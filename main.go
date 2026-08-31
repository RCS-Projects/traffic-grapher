package main

import (
	"embed"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	initConfig()
	app := NewApp()

	staticFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("failed to open static fs: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/scan", app.handleScan)
	mux.HandleFunc("/api/start", app.handleStart)
	mux.HandleFunc("/api/stop", app.handleStop)
	mux.HandleFunc("/api/status", app.handleStatus)
	mux.HandleFunc("/ws", app.handleWebSocket)
	mux.Handle("/", spaHandler(staticFS))

	addr := net.JoinHostPort(host, port)
	log.Printf("Traffic Grapher running on http://%s", addr)
	for _, ip := range listIPs(host, port) {
		log.Printf("  -> try http://%s", ip)
	}

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// spaHandler serves static files and falls back to index.html for SPA routes.
func spaHandler(staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		f, err := staticFS.Open(path)
		if err != nil {
			serveIndexHTML(w, staticFS)
			return
		}
		defer f.Close()
		if stat, err := f.Stat(); err == nil && stat.IsDir() {
			serveIndexHTML(w, staticFS)
			return
		}
		if path == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if path == "service-worker.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveIndexHTML(w http.ResponseWriter, staticFS fs.FS) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func listIPs(host, port string) []string {
	if host != "" && host != "0.0.0.0" {
		return []string{net.JoinHostPort(host, port)}
	}
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				out = append(out, net.JoinHostPort(ipnet.IP.String(), port))
			}
		}
	}
	return out
}
