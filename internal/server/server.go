package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"disk-space-analyser/internal/db"
	ifmt "disk-space-analyser/internal/fmt"
)

//go:embed web
var webFS embed.FS

// DefaultPort is the default TCP port for the HTTP server.
const DefaultPort = 3097

// DirResponse is the JSON-serializable representation of a directory entry.
type DirResponse struct {
	Path          string `json:"path"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	SizeFormatted string `json:"size_formatted"`
	Shallow       bool   `json:"shallow"`
}

// errorResponse is a JSON error envelope.
type errorResponse struct {
	Error string `json:"error"`
}

// Server wraps an http.Server with application-specific state.
type Server struct {
	httpServer *http.Server
	database   *db.DB
	mux        *http.ServeMux
	rootPath   string
}

// New creates a new Server bound to the given database and port.
func New(database *db.DB, port int, rootPath string) *Server {
	s := &Server{
		database: database,
		mux:      http.NewServeMux(),
		rootPath: rootPath,
	}

	s.mux.HandleFunc("/api/summary", s.handleSummary)
	s.mux.HandleFunc("/api/tree", s.handleTree)
	s.mux.HandleFunc("/api/meta", s.handleMeta)
	s.mux.HandleFunc("/report", s.handleReport)
	s.mux.HandleFunc("/", s.handleRoot)

	// Serve embedded static assets at /static/
	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Printf("server: warning: could not create sub FS for web assets: %v", err)
	} else {
		s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(subFS))))
	}

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: s.mux,
	}

	return s
}

// Handler returns the registered mux for direct use in tests.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe starts the HTTP server. When the done channel closes,
// the server shuts down gracefully.
func (s *Server) ListenAndServe(done <-chan struct{}) error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	go func() {
		<-done
		log.Println("server: shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(ctx); err != nil {
			log.Printf("server: shutdown error: %v", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-done:
		return <-errCh
	}
}

// handleSummary returns the top N non-shallow directories sorted by size descending.
// Query params: top (default 20, max 100).
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	top, err := parseTop(r.URL.Query().Get("top"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid top parameter"})
		return
	}

	entries, dbErr := s.database.GetLargestDirs(r.Context(), top)
	if dbErr != nil {
		log.Printf("server: get largest dirs: %v", dbErr)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, toDirResponses(entries))
}

// handleTree returns direct children of the given path.
// Query params: path (default "/"), limit (default 10000), offset (default 0).
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	limit, offset := parsePagination(r.URL.Query())

	entries, dbErr := s.database.GetChildren(r.Context(), path, limit, offset)
	if dbErr != nil {
		log.Printf("server: get children of %s: %v", path, dbErr)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, toDirResponses(entries))
}

// handleReport serves the web report UI.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "report not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// handleMeta returns metadata about the scan, including the root path.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"root": s.rootPath})
}

// handleRoot redirects to /report.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/report", http.StatusFound)
}

// --- helpers ---

func parseTop(raw string) (int, error) {
	if raw == "" {
		return 20, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid top: %s", raw)
	}
	if n > 100 {
		n = 100
	}
	return n, nil
}

func parsePagination(q url.Values) (limit, offset int) {
	limit = 10000
	offset = 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

func toDirResponses(entries []db.DirEntry) []DirResponse {
	res := make([]DirResponse, len(entries))
	for i, e := range entries {
		res[i] = DirResponse{
			Path:          e.Path,
			Name:          e.Name,
			Size:          e.Size,
			SizeFormatted: ifmt.FormatBytes(e.Size),
			Shallow:       e.Shallow,
		}
	}
	return res
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("server: json marshal error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	w.Write(data)
}
