package main

import (
	"net"

	"go.uber.org/zap"
)

// TCP client and server
type TcpServer struct {
	lnr  net.Listener
	addr string
}

type TcpClient struct {
	conn net.TCPConn
	addr string
}

func (c *TcpClient) Connect() (DataChannel, error) {
	cc, err := net.Dial("tcp", c.addr)
	ctcp, _ := cc.(*net.TCPConn)
	if err != nil {
		lg.Debug("TCP connection failed", zap.String("remote", cc.RemoteAddr().String()))
	} else {
		lg.Debug("TCP connected", zap.String("remote", cc.RemoteAddr().String()))
	}
	return ctcp, err
}

func (c *TcpClient) Close() error {
	return c.conn.Close()
}

func (s *TcpServer) Listen() error {
	var err error
	s.lnr, err = net.Listen("tcp", s.addr)
	return err
}

func (s *TcpServer) Accept(dcs chan<- interface{}) error {
	dc, err := s.lnr.Accept()
	if err == nil {
		lg.Debug("New connection ", zap.String("remote", dc.RemoteAddr().String()))
		dcs <- dc
	}
	return err
}

func (s *TcpServer) Close() error {
	return s.lnr.Close()
}
