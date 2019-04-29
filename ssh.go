package main

import (
	"io"
	"net"
	"strconv"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// SSH client and server
type SshClientDataChannel struct {
	sess *ssh.Session
	rdc  io.Reader
	wrc  io.WriteCloser
}

func (sdc SshClientDataChannel) Read(data []byte) (int, error) {
	return sdc.rdc.Read(data)
}

func (sdc SshClientDataChannel) Write(data []byte) (int, error) {
	return sdc.wrc.Write(data)
}

func (sdc SshClientDataChannel) Close() error {
	return sdc.sess.Close()
}

func (sdc SshClientDataChannel) CloseWrite() error {
	return sdc.wrc.Close()
}

type SshClient struct {
	est  bool
	dc   SshClientDataChannel
	addr string
	cfg  ssh.ClientConfig
}

func (c *SshClient) Connect() (DataChannel, error) {
	c.est = false
	lg.Debug("Setting up SSH client connection", zap.String("remote", c.addr))
	cc, err := ssh.Dial("tcp", c.addr, &c.cfg)
	if err != nil {
		lg.Debug("Unable to establish SSH connection", zap.Error(err))
		return nil, err
	}
	c.dc.sess, err = cc.NewSession()
	if err != nil {
		lg.Debug("Unable to get new SSH client session")
		return nil, err
	}
	c.est = true
	c.dc.rdc, err = c.dc.sess.StdoutPipe()
	if err != nil {
		lg.Debug("Unable to get read channel")
		return nil, err
	}
	c.dc.wrc, err = c.dc.sess.StdinPipe()
	if err != nil {
		lg.Debug("Unable to get write channel")
		return nil, err
	}

	err = c.dc.sess.Shell()

	return c.dc, nil
}

func (c *SshClient) Close() error {
	if c.est {
		c.est = false
		return c.dc.Close()
	}
	return nil
}

type SshServer struct {
	lnr  net.Listener
	addr string
	cfg  ssh.ServerConfig
}

func (s *SshServer) Listen() error {
	var err error
	s.lnr, err = net.Listen("tcp", s.addr)
	return err
}

func (s *SshServer) Accept(dcs chan<- interface{}) error {
	tcpConn, err := s.lnr.Accept()
	if err != nil {
		lg.Warn("Accept() failed")
		return err
	}
	_, chans, reqs, err := ssh.NewServerConn(tcpConn, &s.cfg)
	go ssh.DiscardRequests(reqs)
	go func() {
		for newChannel := range chans {
			connection, requests, err := newChannel.Accept()
			if err != nil {
				lg.Debug("Unable to accept channel")
				continue
			}
			lg.Debug("New channel accepted")
			dcs <- connection
			go func() {
				for req := range requests {
					lg.Debug("Request received", zap.String("request", req.Type), zap.String("want-reply", strconv.FormatBool(req.WantReply)))
					switch req.Type {
					case "exec":
						req.Reply(true, nil)
					case "shell":
						req.Reply(true, nil)
					case "pty-req":
						req.Reply(true, nil)
					case "window-change":
						req.Reply(true, nil)
					}
				}
			}()
		}
	}()
	return nil
}

func (s *SshServer) Close() error {
	return s.lnr.Close()
}
