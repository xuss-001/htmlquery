package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/page/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Page One</title></head><body><h1>First page</h1><p>Collected content one.</p></body></html>`)
	})
	mux.HandleFunc("/page/oversized", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><head><title>Oversized Page</title></head><body><p>%s</p></body></html>`,
			strings.Repeat("large response content ", 200))
	})
	mux.HandleFunc("/page/3", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Page Three</title></head><body><h1>Third page</h1><p>Collected content three.</p></body></html>`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	options := htmlquery.LoadOptions{
		Client:           server.Client(),
		MaxResponseBytes: 512,
		MaxDocumentBytes: 1024,
	}
	pages := []string{"/page/1", "/page/oversized", "/page/3"}
	for _, path := range pages {
		doc, err := htmlquery.LoadURLWithOptions(ctx, server.URL+path, options)
		if errors.Is(err, htmlquery.ErrResponseTooLarge) || errors.Is(err, htmlquery.ErrDocumentTooLarge) {
			fmt.Printf("skip %s: %v\n", path, err)
			continue
		}
		if err != nil {
			var statusErr *htmlquery.HTTPStatusError
			if errors.As(err, &statusErr) {
				log.Printf("skip %s: HTTP %d at %s", path, statusErr.StatusCode, statusErr.URL)
				continue
			}
			log.Printf("skip %s: %v", path, err)
			continue
		}

		title := htmlquery.FindOne(doc, "//title")
		heading := htmlquery.FindOne(doc, "//h1")
		fmt.Printf("collected %s: title=%q heading=%q\n",
			path, htmlquery.InnerText(title), htmlquery.InnerText(heading))
	}
}
