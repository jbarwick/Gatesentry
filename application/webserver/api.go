package gatesentryWebserver

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

var (
	adminServerMu sync.Mutex
	adminServer   *http.Server
)

type GsWeb struct {
	router   *mux.Router // root router (handles redirect, serves subrouter)
	sub      *mux.Router // subrouter mounted at basePath — all routes go here
	basePath string
}

type HttpHandlerFunc func(http.ResponseWriter, *http.Request)

func NewGsWeb(basePath string) *GsWeb {
	root := mux.NewRouter()

	var sub *mux.Router
	if basePath == "/" {
		sub = root
	} else {
		sub = root.PathPrefix(basePath).Subrouter()
		// Redirect bare root to the base path
		root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, basePath+"/", http.StatusFound)
		})
	}

	// Apply CORS middleware globally to all API routes
	sub.Use(corsMiddleware)

	return &GsWeb{
		router:   root,
		sub:      sub,
		basePath: basePath,
	}
}

func (g *GsWeb) Get(path string, handlerOrMiddleware interface{}, optionalHandler ...HttpHandlerFunc) {
	switch h := handlerOrMiddleware.(type) {
	case HttpHandlerFunc:
		g.sub.Handle(path, http.HandlerFunc(h)).Methods("GET")
	case mux.MiddlewareFunc:
		if len(optionalHandler) > 0 {
			g.sub.Handle(path, h(http.HandlerFunc(optionalHandler[0]))).Methods("GET")
		} else {
			panic("middleware provided but no handler function")
		}
	default:
		panic("unsupported type provided to GET method")
	}
}

func (g *GsWeb) Post(path string, handlerOrMiddleware interface{}, optionalHandler ...HttpHandlerFunc) {
	switch h := handlerOrMiddleware.(type) {
	case HttpHandlerFunc:
		g.sub.Handle(path, http.HandlerFunc(h)).Methods("POST")
	case mux.MiddlewareFunc:
		if len(optionalHandler) > 0 {
			g.sub.Handle(path, h(http.HandlerFunc(optionalHandler[0]))).Methods("POST")
		} else {
			panic("middleware provided but no handler function")
		}
	default:
		panic("unsupported type provided to POST method")
	}
}

func (g *GsWeb) Put(path string, handlerOrMiddleware interface{}, optionalHandler ...HttpHandlerFunc) {
	switch h := handlerOrMiddleware.(type) {
	case HttpHandlerFunc:
		g.sub.Handle(path, http.HandlerFunc(h)).Methods("PUT")
	case mux.MiddlewareFunc:
		if len(optionalHandler) > 0 {
			g.sub.Handle(path, h(http.HandlerFunc(optionalHandler[0]))).Methods("PUT")
		} else {
			panic("middleware provided but no handler function")
		}
	default:
		panic("unsupported type provided to PUT method")
	}
}

func (g *GsWeb) Delete(path string, handlerOrMiddleware interface{}, optionalHandler ...HttpHandlerFunc) {
	switch h := handlerOrMiddleware.(type) {
	case HttpHandlerFunc:
		g.sub.Handle(path, http.HandlerFunc(h)).Methods("DELETE")
	case mux.MiddlewareFunc:
		if len(optionalHandler) > 0 {
			g.sub.Handle(path, h(http.HandlerFunc(optionalHandler[0]))).Methods("DELETE")
		} else {
			panic("middleware provided but no handler function")
		}
	default:
		panic("unsupported type provided to DELETE method")
	}
}

func (g *GsWeb) ListenAndServe(port string) error {
	srv := &http.Server{
		Addr:              port,
		Handler:           g.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		// WriteTimeout left unset so SSE streams are not killed.
	}
	adminServerMu.Lock()
	adminServer = srv
	adminServerMu.Unlock()
	return srv.ListenAndServe()
}

// ShutdownAdmin stops the admin HTTP server. Safe if it was never started.
func ShutdownAdmin(ctx context.Context) error {
	adminServerMu.Lock()
	srv := adminServer
	adminServerMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
