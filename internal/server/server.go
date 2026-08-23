package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 30 * time.Second
	sessionTokenBytes = 32
)

type Server struct {
	httpServer   *http.Server
	listener     net.Listener
	done         chan error
	sessionToken string
}

func Start(files fs.FS, dependencies Dependencies) (*Server, error) {
	if dependencies.Rules == nil {
		return nil, errors.New("rule manager is required")
	}
	sessionToken, err := generateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on loopback address: %w", err)
	}

	handler := newHandler(listener.Addr().String(), sessionToken, files, dependencies)
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	localServer := &Server{
		httpServer:   httpServer,
		listener:     listener,
		done:         make(chan error, 1),
		sessionToken: sessionToken,
	}

	go func() {
		serveErr := httpServer.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		localServer.done <- serveErr
		close(localServer.done)
	}()

	return localServer, nil
}

func (s *Server) URL() string {
	return "http://" + s.listener.Addr().String() + "/"
}

func (s *Server) DashboardURL() string {
	return s.URL() + "#token=" + url.QueryEscape(s.sessionToken)
}

func (s *Server) Done() <-chan error {
	return s.done
}

func (s *Server) Close(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func generateSessionToken() (string, error) {
	randomBytes := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}
