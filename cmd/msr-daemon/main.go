package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
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
	mux.HandleFunc("/api/install-apk", srv.handleInstallAPK)
	mux.HandleFunc("/api/files", srv.handleFiles)
	mux.HandleFunc("/api/files/upload", srv.handleFileUpload)
	mux.HandleFunc("/api/files/download", srv.handleFileDownload)
	mux.HandleFunc("/api/shell", srv.handleShell)
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

func (s *server) handleInstallAPK(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthorized(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uploadDir := filepath.Join(s.cfg.StateDir, "uploads")
	apkPath, originalName, err := saveMultipartFile(r, uploadDir, "apk", ".apk")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer os.Remove(apkPath)

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	out, runErr := exec.CommandContext(ctx, "pm", "install", "-r", "-t", apkPath).CombinedOutput()
	status := "ok"
	if runErr != nil {
		status = "error"
	}
	writeJSON(w, map[string]any{
		"status": status,
		"name":   originalName,
		"output": strings.TrimSpace(string(out)),
		"error":  errorString(runErr),
	})
}

func (s *server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthorized(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := cleanDevicePath(r.URL.Query().Get("path"), "/sdcard")
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		writeJSON(w, map[string]any{
			"path":    path,
			"entries": []fileEntry{entryFor(path, info)},
		})
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	items := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		itemInfo, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, entryFor(filepath.Join(path, entry.Name()), itemInfo))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	writeJSON(w, map[string]any{
		"path":    path,
		"parent":  parentDevicePath(path),
		"entries": items,
	})
}

func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthorized(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	targetDir := cleanDevicePath(r.URL.Query().Get("path"), "/sdcard/Download")
	savedPath, originalName, err := saveMultipartFile(r, targetDir, "file", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"status": "ok",
		"name":   originalName,
		"path":   savedPath,
	})
}

func (s *server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthorized(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := cleanDevicePath(r.URL.Query().Get("path"), "")
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "cannot download directory", http.StatusBadRequest)
		return
	}
	name := filepath.Base(path)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeFile(w, r, path)
}

func (s *server) handleShell(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthorized(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/system/bin/sh", "-c", req.Command).CombinedOutput()
	status := "ok"
	if err != nil {
		status = "error"
	}
	writeJSON(w, map[string]any{
		"status":   status,
		"command":  req.Command,
		"output":   string(out),
		"error":    errorString(err),
		"timedOut": ctx.Err() == context.DeadlineExceeded,
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

func (s *server) requireAuthorized(w http.ResponseWriter, r *http.Request) bool {
	if s.authorized(r) {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"modTime"`
}

func entryFor(path string, info os.FileInfo) fileEntry {
	return fileEntry{
		Name:    info.Name(),
		Path:    path,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().Format(time.RFC3339),
	}
}

func cleanDevicePath(path, fallback string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = fallback
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return filepath.Clean(path)
}

func parentDevicePath(path string) string {
	path = filepath.Clean(path)
	if path == "/" {
		return ""
	}
	return filepath.Dir(path)
}

func saveMultipartFile(r *http.Request, targetDir, fieldName, fallbackExt string) (string, string, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", "", err
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		if part.FormName() != fieldName {
			_ = part.Close()
			continue
		}
		originalName := filepath.Base(part.FileName())
		if originalName == "." || originalName == "/" || originalName == "" {
			originalName = fieldName + fallbackExt
		}
		targetPath := filepath.Join(targetDir, originalName)
		if fallbackExt != "" {
			tmp, err := os.CreateTemp(targetDir, "upload-*"+fallbackExt)
			if err != nil {
				_ = part.Close()
				return "", "", err
			}
			targetPath = tmp.Name()
			if _, err := io.Copy(tmp, part); err != nil {
				_ = tmp.Close()
				_ = part.Close()
				return "", "", err
			}
			if err := tmp.Close(); err != nil {
				_ = part.Close()
				return "", "", err
			}
			_ = part.Close()
			return targetPath, originalName, nil
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = part.Close()
			return "", "", err
		}
		if _, err := io.Copy(out, part); err != nil {
			_ = out.Close()
			_ = part.Close()
			return "", "", err
		}
		if err := out.Close(); err != nil {
			_ = part.Close()
			return "", "", err
		}
		_ = part.Close()
		return targetPath, originalName, nil
	}
	return "", "", fmt.Errorf("missing multipart field %q", fieldName)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
