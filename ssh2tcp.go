package main

import (
	"errors"
	"flag"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

var lg *zap.Logger

type DataChannel interface {
	Read(data []byte) (int, error)
	Write(data []byte) (int, error)
	Close() error
	CloseWrite() error
}

type Server interface {
	Listen() error
	Accept(dcs chan<- interface{}) error
	Close() error
}

type Client interface {
	Connect() (DataChannel, error)
	Close() error
}

func init() {
	var err error
	lg, err = zap.NewProduction()
	if err != nil {
		panic(err)
	}
}

func doServer(s Server, dcs chan<- interface{}, wg *sync.WaitGroup, done <-chan struct{}) {
	defer wg.Done()
	err := s.Listen()
	if err != nil {
		lg.Debug("Listen() failed")
		return
	}
	for {
		err := s.Accept(dcs)
		if err != nil {
			lg.Debug("Accept() failed")
			return
		}
		select {
		case <-done:
			lg.Debug("server done")
			return
		default:
		}
	}

	return
}

func newServerChannel(c Client, dcs <-chan interface{}, wg *sync.WaitGroup, done <-chan struct{}) {
	defer wg.Done()
	for {
		select {
		case dc := <-dcs:
			lg.Debug("New datachannel received")
			sdc, ok := dc.(DataChannel)
			if ok {
				wg.Add(1)
				go setupRelay(c, sdc, wg, done)
			}
		case <-done:
			lg.Debug("Done")
			return
		}
	}
}

func setupRelay(c Client, sdc DataChannel, wg *sync.WaitGroup, done <-chan struct{}) {
	defer wg.Done()

	cdc, err := c.Connect()
	if err != nil {
		sdc.Close()
		return
	}
	wg.Add(2)

	var once sync.Once

	closer := func() {
		cdc.Close()
		sdc.Close()
	}

	go func() {
		defer wg.Done()
		io.Copy(cdc, sdc)
		lg.Debug("s2c done")
		once.Do(closer)
	}()
	go func() {
		defer wg.Done()
		io.Copy(sdc, cdc)
		lg.Debug("c2s done")
		once.Do(closer)
	}()

	for {
		select {
		case <-done:
			lg.Debug("done")
			once.Do(closer)
			return
		}
	}
}

func initClient(u *url.URL, ca string) (Client, error) {
	switch u.Scheme {
	case "tcp":
		tc := TcpClient{}
		tc.addr = u.Host
		return &tc, nil
	case "ssh":
		sc := SshClient{}
		var sshUser string
		var sshPass string
		var ok bool
		if ca != "" {
			sc.addr = ca + ":22"
			sshUser = u.User.Username() + "@" + u.Host
		} else {
			sc.addr = u.Host
			sshUser = u.User.Username()
		}
		sshPass, ok = u.User.Password()
		if !ok {
			sshPass = "12345678"
		}
		sc.cfg = ssh.ClientConfig{
			User: sshUser,
			Auth: []ssh.AuthMethod{
				ssh.Password(sshPass),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
		return &sc, nil
	default:
		return nil, errors.New("Invalid scheme")
	}

	return nil, errors.New("Invalid scheme")
}

func initServer(u *url.URL, hostkey string) (Server, error) {
	switch u.Scheme {
	case "tcp":
		ts := TcpServer{}
		ts.addr = u.Host
		return &ts, nil
	case "ssh":
		ss := SshServer{}
		ss.addr = u.Host
		ss.cfg = ssh.ServerConfig{
			PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
				return nil, nil
			},
		}
		privKey, err := ioutil.ReadFile(hostkey)
		if err == nil {
			priv, err := ssh.ParsePrivateKey(privKey)
			if err != nil {
				lg.Warn("Unable to parse private key")
				return nil, err
			}
			ss.cfg.AddHostKey(priv)
		} else {
			lg.Warn("Unable to read hostkey")
			return nil, err
		}
		return &ss, nil
	default:
		return nil, errors.New("Invalid scheme")
	}

	return nil, errors.New("Invalid scheme")
}

func main() {

	listenURL := flag.String("listen", "", "Listen address, eg. ssh://127.0.0.1:1234")
	connectURL := flag.String("connect", "", "Connect address, eg. tcp://127.0.0.1:4321")
	caURL := flag.String("ca", "", "CA address")
	hostKey := flag.String("hostkey", "", "Host private key for SSH listener")
	debug := flag.Bool("debug", false, "true to enable debug logging")

	flag.Parse()
	if *debug {
		lg = zap.NewExample()
	}
	if *listenURL == "" || *connectURL == "" {
		panic("'listen' or 'connect' has to be specified")
	}

	var listen *url.URL
	var connect *url.URL
	var err error
	var client Client
	var server Server

	listen, err = url.Parse(*listenURL)
	if err != nil {
		panic("Unable to parse listen URL")
	}
	connect, err = url.Parse(*connectURL)
	if err != nil {
		panic("Unable to parse connect URL")
	}

	var wg sync.WaitGroup
	done := make(chan struct{})
	sdcs := make(chan interface{})

	client, err = initClient(connect, *caURL)
	if err != nil {
		panic("Unable to create client")
	}
	server, err = initServer(listen, *hostKey)
	if err != nil {
		panic("Unable to create server")
	}
	wg.Add(1)
	go doServer(server, sdcs, &wg, done)
	wg.Add(1)
	go newServerChannel(client, sdcs, &wg, done)

	signal_chan := make(chan os.Signal, 1)
	signal.Notify(signal_chan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	quit := false
	for !quit {
		select {
		case <-signal_chan:
			lg.Debug("Signal received")
			quit = true
		}
	}

	close(done)
	client.Close()
	server.Close()
	lg.Debug("Waiting for all done")
	wg.Wait()
	lg.Debug("All done")
}
