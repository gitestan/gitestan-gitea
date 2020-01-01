// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// This code is highly inspired by endless go

package graceful

import (
	"crypto/tls"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"code.gitea.io/gitea/modules/log"
)

var (
	// DefaultReadTimeOut default read timeout
	DefaultReadTimeOut time.Duration
	// DefaultWriteTimeOut default write timeout
	DefaultWriteTimeOut time.Duration
	// DefaultMaxHeaderBytes default max header bytes
	DefaultMaxHeaderBytes int
)

func init() {
	DefaultMaxHeaderBytes = 0 // use http.DefaultMaxHeaderBytes - which currently is 1 << 20 (1MB)
}

// ServeFunction represents a listen.Accept loop
type ServeFunction = func(net.Listener) error

// Server represents our graceful server
type Server struct {
	network     string
	address     string
	listener    net.Listener
	wg          sync.WaitGroup
	state       state
	lock        *sync.RWMutex
	BeforeBegin func(network, address string)
	OnShutdown  func()
}

// NewServer creates a server on network at provided address
func NewServer(network, address string) *Server {
	if GetManager().IsChild() {
		log.Info("Restarting new server: %s:%s on PID: %d", network, address, os.Getpid())
	} else {
		log.Info("Starting new server: %s:%s on PID: %d", network, address, os.Getpid())
	}
	srv := &Server{
		wg:      sync.WaitGroup{},
		state:   stateInit,
		lock:    &sync.RWMutex{},
		network: network,
		address: address,
	}

	srv.BeforeBegin = func(network, addr string) {
		log.Debug("Starting server on %s:%s (PID: %d)", network, addr, syscall.Getpid())
	}

	return srv
}

// ListenAndServe listens on the provided network address and then calls Serve
// to handle requests on incoming connections.
func (srv *Server) ListenAndServe(serve ServeFunction) error {
	go srv.awaitShutdown()

	l, err := GetListener(srv.network, srv.address)
	if err != nil {
		log.Error("Unable to GetListener: %v", err)
		return err
	}

	srv.listener = newWrappedListener(l, srv)

	srv.BeforeBegin(srv.network, srv.address)

	return srv.Serve(serve)
}

// ListenAndServeTLS listens on the provided network address and then calls
// Serve to handle requests on incoming TLS connections.
//
// Filenames containing a certificate and matching private key for the server must
// be provided. If the certificate is signed by a certificate authority, the
// certFile should be the concatenation of the server's certificate followed by the
// CA's certificate.
func (srv *Server) ListenAndServeTLS(certFile, keyFile string, serve ServeFunction) error {
	config := &tls.Config{}
	if config.NextProtos == nil {
		config.NextProtos = []string{"http/1.1"}
	}

	config.Certificates = make([]tls.Certificate, 1)
	var err error
	config.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Error("Failed to load https cert file %s for %s:%s: %v", certFile, srv.network, srv.address, err)
		return err
	}
	return srv.ListenAndServeTLSConfig(config, serve)
}

// ListenAndServeTLSConfig listens on the provided network address and then calls
// Serve to handle requests on incoming TLS connections.
func (srv *Server) ListenAndServeTLSConfig(tlsConfig *tls.Config, serve ServeFunction) error {
	go srv.awaitShutdown()

	l, err := GetListener(srv.network, srv.address)
	if err != nil {
		log.Error("Unable to get Listener: %v", err)
		return err
	}

	wl := newWrappedListener(l, srv)
	srv.listener = tls.NewListener(wl, tlsConfig)

	srv.BeforeBegin(srv.network, srv.address)

	return srv.Serve(serve)
}

// Serve accepts incoming HTTP connections on the wrapped listener l, creating a new
// service goroutine for each. The service goroutines read requests and then call
// handler to reply to them. Handler is typically nil, in which case the
// DefaultServeMux is used.
//
// In addition to the standard Serve behaviour each connection is added to a
// sync.Waitgroup so that all outstanding connections can be served before shutting
// down the server.
func (srv *Server) Serve(serve ServeFunction) error {
	defer log.Debug("Serve() returning... (PID: %d)", syscall.Getpid())
	srv.setState(stateRunning)
	GetManager().RegisterServer()
	err := serve(srv.listener)
	log.Debug("Waiting for connections to finish... (PID: %d)", syscall.Getpid())
	srv.wg.Wait()
	srv.setState(stateTerminate)
	GetManager().ServerDone()
	// use of closed means that the listeners are closed - i.e. we should be shutting down - return nil
	if err != nil && strings.Contains(err.Error(), "use of closed") {
		return nil
	}
	return err
}

func (srv *Server) getState() state {
	srv.lock.RLock()
	defer srv.lock.RUnlock()

	return srv.state
}

func (srv *Server) setState(st state) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.state = st
}

type filer interface {
	File() (*os.File, error)
}

type wrappedListener struct {
	net.Listener
	stopped bool
	server  *Server
}

func newWrappedListener(l net.Listener, srv *Server) *wrappedListener {
	return &wrappedListener{
		Listener: l,
		server:   srv,
	}
}

func (wl *wrappedListener) Accept() (net.Conn, error) {
	var c net.Conn
	// Set keepalive on TCPListeners connections.
	if tcl, ok := wl.Listener.(*net.TCPListener); ok {
		tc, err := tcl.AcceptTCP()
		if err != nil {
			return nil, err
		}
		_ = tc.SetKeepAlive(true)                  // see http.tcpKeepAliveListener
		_ = tc.SetKeepAlivePeriod(3 * time.Minute) // see http.tcpKeepAliveListener
		c = tc
	} else {
		var err error
		c, err = wl.Listener.Accept()
		if err != nil {
			return nil, err
		}
	}

	c = wrappedConn{
		Conn:   c,
		server: wl.server,
	}

	wl.server.wg.Add(1)
	return c, nil
}

func (wl *wrappedListener) Close() error {
	if wl.stopped {
		return syscall.EINVAL
	}

	wl.stopped = true
	return wl.Listener.Close()
}

func (wl *wrappedListener) File() (*os.File, error) {
	// returns a dup(2) - FD_CLOEXEC flag *not* set so the listening socket can be passed to child processes
	return wl.Listener.(filer).File()
}

type wrappedConn struct {
	net.Conn
	server *Server
}

func (w wrappedConn) Close() error {
	err := w.Conn.Close()
	if err == nil {
		w.server.wg.Done()
	}
	return err
}
