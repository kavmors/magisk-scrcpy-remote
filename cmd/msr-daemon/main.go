package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/kavmors/magisk-scrcpy-remote/internal/config"
	"github.com/kavmors/magisk-scrcpy-remote/internal/duallistener"
	"github.com/kavmors/magisk-scrcpy-remote/internal/wsstream"
	"nhooyr.io/websocket"
)

type server struct {
	cfg         config.Config
	webDir      string
	token       string
	replaceWait time.Duration

	mu           sync.Mutex
	active       bool
	activeCancel context.CancelFunc
	activeDone   chan struct{}
}

func main() {
	var configPath string
	var webDir string
	var moduleDir string
	flag.StringVar(&configPath, "config", "", "config file path")
	flag.StringVar(&webDir, "web", "web", "web assets directory")
	flag.StringVar(&moduleDir, "module", "", "Magisk module directory containing the token file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if moduleDir == "" {
		moduleDir = filepath.Dir(filepath.Clean(webDir))
	}
	if err := config.EnsureState(cfg); err != nil {
		log.Fatalf("prepare state: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tokenPath, err := config.ModuleTokenPath(moduleDir)
	if err != nil {
		log.Fatalf("resolve token path: %v", err)
	}
	tokenLocalPath, err := config.ModuleTokenLocalPath(moduleDir)
	if err != nil {
		log.Fatalf("resolve token.local path: %v", err)
	}
	log.Printf("waiting for non-empty token file: %s or %s", tokenPath, tokenLocalPath)
	token, err := config.WaitModuleToken(ctx, moduleDir, 2*time.Second)
	if err != nil {
		log.Fatalf("wait token: %v", err)
	}
	log.Printf("auth token loaded from module token file")

	srv := &server{cfg: cfg, webDir: webDir, token: token, replaceWait: 5 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/ws", srv.handleWSStream)
	mux.HandleFunc("/ws-stream", srv.handleWSStream)
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	httpsServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		_ = httpsServer.Shutdown(shutdownCtx)
	}()

	httpLn, httpsLn, err := duallistener.Listen(cfg.Listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	tlsConfig, err := duallistener.TLSConfig()
	if err != nil {
		log.Fatalf("tls config: %v", err)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- httpServer.Serve(httpLn) }()
	go func() { errCh <- httpsServer.Serve(tls.NewListener(httpsLn, tlsConfig)) }()

	log.Printf("serving %s on http://%s and https://%s", filepath.Clean(webDir), cfg.Listen, cfg.Listen)
	log.Printf("module dir: %s", filepath.Clean(moduleDir))
	if err := <-errCh; err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server: %v", err)
	}
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"active":        active,
		"tokenRequired": s.token != "",
		"audio":         s.cfg.Audio.Enabled,
	})
}

func (s *server) handleWSStream(w http.ResponseWriter, r *http.Request) {
	s.handleExclusiveWS(w, r, func(ctx context.Context, ws *websocket.Conn) error {
		return wsstream.Run(ctx, ws, s.cfg)
	})
}

func (s *server) handleExclusiveWS(w http.ResponseWriter, r *http.Request, run func(context.Context, *websocket.Conn) error) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("websocket accept: %v", err)
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	done, ok := s.beginSession(ctx, cancel)
	if !ok {
		_ = ws.Close(websocket.StatusTryAgainLater, "busy")
		return
	}
	defer s.endSession(done)

	if err := run(ctx, ws); err != nil {
		log.Printf("session ended: %v", err)
		_ = ws.Close(websocket.StatusInternalError, err.Error())
	}
}

func (s *server) beginSession(ctx context.Context, cancel context.CancelFunc) (chan struct{}, bool) {
	s.mu.Lock()
	if !s.active {
		done := make(chan struct{})
		s.active = true
		s.activeCancel = cancel
		s.activeDone = done
		s.mu.Unlock()
		return done, true
	}
	oldCancel := s.activeCancel
	oldDone := s.activeDone
	s.mu.Unlock()

	if oldCancel == nil || oldDone == nil {
		return nil, false
	}

	log.Printf("replacing active session with a new authorized connection")
	oldCancel()

	select {
	case <-oldDone:
	case <-ctx.Done():
		return nil, false
	case <-time.After(s.replaceWait):
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return nil, false
	}
	done := make(chan struct{})
	s.active = true
	s.activeCancel = cancel
	s.activeDone = done
	return done, true
}

func (s *server) endSession(done chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeDone == done {
		s.active = false
		s.activeCancel = nil
		s.activeDone = nil
		close(done)
	}
}

func (s *server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-MSR-Token")
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
