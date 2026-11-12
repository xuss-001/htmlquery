package htmlquery

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const optionsTestDocument = `<!DOCTYPE html><html><head><title>options page</title></head><body><p>content</p></body></html>`

func newOptionsTestClient(status int, body io.Reader, header http.Header) (*http.Client, *trackedResponseBody) {
	tracked := &trackedResponseBody{Reader: body}
	if header == nil {
		header = make(http.Header)
	}
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     header,
			Body:       tracked,
			Request:    req,
		}, nil
	})}
	return client, tracked
}

func assertDocumentTitle(t *testing.T, doc *html.Node, want string) {
	t.Helper()
	if doc == nil {
		t.Fatal("document is nil")
	}
	title := FindOne(doc, "//title")
	if title == nil {
		t.Fatal("title element not found")
	}
	if got := InnerText(title); got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func gzipTestData(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := io.WriteString(writer, data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zlibTestData(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	if _, err := io.WriteString(writer, data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeTestDocument(t *testing.T, body []byte, contentType string) []byte {
	t.Helper()
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ioutil.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestLoadURLWithOptionsParameterErrorsBeforeRequest(t *testing.T) {
	var requests int32
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return nil, errors.New("request must not be sent")
	})}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name    string
		ctx     context.Context
		options LoadOptions
	}{
		{"nil context", nil, LoadOptions{Client: client}},
		{"negative response limit", context.Background(), LoadOptions{Client: client, MaxResponseBytes: -1}},
		{"negative document limit", context.Background(), LoadOptions{Client: client, MaxDocumentBytes: -1}},
		{"already canceled", canceled, LoadOptions{Client: client}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := LoadURLWithOptions(tc.ctx, "https://example.invalid/page", tc.options)
			if err == nil {
				t.Fatal("expected an error")
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			if tc.name == "already canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("Transport requests = %d, want 0", got)
	}
}

func TestLoadURLWithOptionsResponseLimit(t *testing.T) {
	body := []byte(optionsTestDocument)

	t.Run("exactly at limit", func(t *testing.T) {
		counting := &countingReader{reader: bytes.NewReader(body)}
		client, tracked := newOptionsTestClient(http.StatusOK, counting, http.Header{
			"Content-Type": []string{"text/html; charset=utf-8"},
		})
		doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
			Client:           client,
			MaxResponseBytes: int64(len(body)),
		})
		if err != nil {
			t.Fatalf("LoadURLWithOptions() error = %v", err)
		}
		assertDocumentTitle(t, doc, "options page")
		if counting.read != int64(len(body)) {
			t.Fatalf("body bytes read = %d, want %d", counting.read, len(body))
		}
		if tracked.closes != 1 {
			t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
		}
	})

	t.Run("one byte over limit", func(t *testing.T) {
		max := int64(len(body) - 1)
		counting := &countingReader{reader: bytes.NewReader(body)}
		client, tracked := newOptionsTestClient(http.StatusOK, counting, http.Header{
			"Content-Type": []string{"text/html; charset=utf-8"},
		})
		doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
			Client:           client,
			MaxResponseBytes: max,
		})
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("error = %v, want ErrResponseTooLarge", err)
		}
		if doc != nil {
			t.Fatal("document is non-nil on error")
		}
		if maxRead := max + 1; counting.read > maxRead {
			t.Fatalf("body bytes read = %d, want at most %d", counting.read, maxRead)
		}
		if tracked.closes != 1 {
			t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
		}
	})

	t.Run("zero disables limit", func(t *testing.T) {
		client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(body), http.Header{
			"Content-Type": []string{"text/html; charset=utf-8"},
		})
		doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{Client: client})
		if err != nil {
			t.Fatalf("LoadURLWithOptions() error = %v", err)
		}
		assertDocumentTitle(t, doc, "options page")
		if tracked.closes != 1 {
			t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
		}
	})
}

func TestLoadURLWithOptionsDocumentLimit(t *testing.T) {
	body := []byte(optionsTestDocument)
	client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(body), http.Header{
		"Content-Type": []string{"text/html; charset=utf-8"},
	})
	doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxDocumentBytes: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("LoadURLWithOptions() error = %v", err)
	}
	assertDocumentTitle(t, doc, "options page")
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}

	client, tracked = newOptionsTestClient(http.StatusOK, bytes.NewReader(body), http.Header{
		"Content-Type": []string{"text/html; charset=utf-8"},
	})
	doc, err = LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxDocumentBytes: int64(len(body) - 1),
	})
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("error = %v, want ErrDocumentTooLarge", err)
	}
	if doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsLargeLimits(t *testing.T) {
	body := []byte(optionsTestDocument)
	client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(body), http.Header{
		"Content-Type": []string{"text/html; charset=utf-8"},
	})
	doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxResponseBytes: math.MaxInt64,
		MaxDocumentBytes: math.MaxInt64,
	})
	if err != nil {
		t.Fatalf("LoadURLWithOptions() error = %v", err)
	}
	assertDocumentTitle(t, doc, "options page")
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsCompressedLimits(t *testing.T) {
	document := optionsTestDocument
	for _, tc := range []struct {
		name     string
		encoding string
		body     []byte
	}{
		{"gzip", "gzip", gzipTestData(t, document)},
		{"zlib deflate", "deflate", zlibTestData(t, document)},
	} {
		t.Run(tc.name+" response limit", func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxResponseBytes: int64(len(tc.body)),
			})
			if err != nil {
				t.Fatalf("LoadURLWithOptions() error = %v", err)
			}
			assertDocumentTitle(t, doc, "options page")
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})

		t.Run(tc.name+" response one byte over limit", func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxResponseBytes: int64(len(tc.body) - 1),
			})
			if !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("error = %v, want ErrResponseTooLarge", err)
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})

		t.Run(tc.name+" response limit in compressed header", func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxResponseBytes: 1,
			})
			if !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("error = %v, want ErrResponseTooLarge", err)
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})

		t.Run(tc.name+" document limit", func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxResponseBytes: int64(len(tc.body)),
				MaxDocumentBytes: int64(len(document)),
			})
			if err != nil {
				t.Fatalf("LoadURLWithOptions() error = %v", err)
			}
			assertDocumentTitle(t, doc, "options page")

			client, tracked = newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err = LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxResponseBytes: int64(len(tc.body)),
				MaxDocumentBytes: int64(len(document) - 1),
			})
			if !errors.Is(err, ErrDocumentTooLarge) {
				t.Fatalf("error = %v, want ErrDocumentTooLarge", err)
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})

		t.Run(tc.name+" document limit of one byte", func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxDocumentBytes: 1,
			})
			if !errors.Is(err, ErrDocumentTooLarge) {
				t.Fatalf("error = %v, want ErrDocumentTooLarge", err)
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})
	}
}

func TestLoadURLWithOptionsTransportAutoDecompressionCountsProvidedBytes(t *testing.T) {
	document := []byte(optionsTestDocument)
	for _, tc := range []struct {
		name string
		max  int64
		ok   bool
	}{
		{"exact decompressed size", int64(len(document)), true},
		{"one byte over decompressed size", int64(len(document) - 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracked := &trackedResponseBody{Reader: bytes.NewReader(document)}
			client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:   http.StatusOK,
					Header:       http.Header{"Content-Encoding": []string{"gzip"}, "Content-Type": []string{"text/html; charset=utf-8"}},
					Body:         tracked,
					Uncompressed: true,
					Request:      req,
				}, nil
			})}
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
				Client:           client,
				MaxResponseBytes: tc.max,
			})
			if tc.ok {
				if err != nil {
					t.Fatalf("LoadURLWithOptions() error = %v", err)
				}
				assertDocumentTitle(t, doc, "options page")
			} else {
				if !errors.Is(err, ErrResponseTooLarge) {
					t.Fatalf("error = %v, want ErrResponseTooLarge", err)
				}
				if doc != nil {
					t.Fatal("document is non-nil on error")
				}
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})
	}
}

func TestLoadURLWithOptionsCharsetConversionLimit(t *testing.T) {
	body := append([]byte(`<html><head><title>caf`), 0xe9)
	body = append(body, []byte(`</title></head><body><p>`)...)
	body = append(body, bytes.Repeat([]byte{0xe9}, 32)...)
	body = append(body, []byte(`</p></body></html>`)...)
	const contentType = "text/html; charset=windows-1252"
	decoded := decodeTestDocument(t, body, contentType)
	if len(decoded) <= len(body) {
		t.Fatalf("decoded size = %d, source size = %d; test data must grow during conversion", len(decoded), len(body))
	}

	client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(body), http.Header{
		"Content-Type": []string{contentType},
	})
	doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxDocumentBytes: int64(len(decoded)),
	})
	if err != nil {
		t.Fatalf("LoadURLWithOptions() error = %v", err)
	}
	assertDocumentTitle(t, doc, "café")
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}

	client, tracked = newOptionsTestClient(http.StatusOK, bytes.NewReader(body), http.Header{
		"Content-Type": []string{contentType},
	})
	doc, err = LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxDocumentBytes: int64(len(decoded) - 1),
	})
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("error = %v, want ErrDocumentTooLarge", err)
	}
	if doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsContentLengthDoesNotAffectLimit(t *testing.T) {
	body := []byte(optionsTestDocument)
	tracked := &trackedResponseBody{Reader: bytes.NewReader(body)}
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:          tracked,
			ContentLength: 1,
			Request:       req,
		}, nil
	})}

	doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxResponseBytes: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("LoadURLWithOptions() error = %v", err)
	}
	assertDocumentTitle(t, doc, "options page")

	tracked = &trackedResponseBody{Reader: bytes.NewReader(body)}
	client = &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:          tracked,
			ContentLength: 1,
			Request:       req,
		}, nil
	})}
	doc, err = LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{
		Client:           client,
		MaxResponseBytes: int64(len(body) - 1),
	})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v, want ErrResponseTooLarge", err)
	}
	if doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsBodyErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.Reader
	}{
		{"plain read failure", failedResponseReader{}},
		{"length mismatch", errorReader{err: io.ErrUnexpectedEOF}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, tc.body, http.Header{
				"Content-Type": []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{Client: client})
			if err == nil {
				t.Fatal("expected an error")
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			want := io.ErrClosedPipe
			if tc.name == "length mismatch" {
				want = io.ErrUnexpectedEOF
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, want)
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})
	}
}

func TestLoadURLWithOptionsCompressionErrors(t *testing.T) {
	validGzip := gzipTestData(t, optionsTestDocument)
	badChecksum := append([]byte(nil), validGzip...)
	badChecksum[len(badChecksum)-5] ^= 0xff
	validZlib := zlibTestData(t, optionsTestDocument)
	badZlibChecksum := append([]byte(nil), validZlib...)
	badZlibChecksum[len(badZlibChecksum)-1] ^= 0xff

	for _, tc := range []struct {
		name, encoding string
		body           []byte
	}{
		{"invalid gzip", "gzip", []byte("invalid gzip")},
		{"gzip checksum", "gzip", badChecksum},
		{"invalid deflate", "deflate", []byte("invalid deflate")},
		{"zlib checksum", "deflate", badZlibChecksum},
		{"unsupported encoding", "br", []byte(optionsTestDocument)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, tracked := newOptionsTestClient(http.StatusOK, bytes.NewReader(tc.body), http.Header{
				"Content-Encoding": []string{tc.encoding},
				"Content-Type":     []string{"text/html; charset=utf-8"},
			})
			doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{Client: client})
			if err == nil {
				t.Fatal("expected an error")
			}
			if doc != nil {
				t.Fatal("document is non-nil on error")
			}
			if tracked.closes != 1 {
				t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
			}
		})
	}
}

func TestLoadURLWithOptionsHTTPStatusErrorBeforeBodyRead(t *testing.T) {
	client, tracked := newOptionsTestClient(http.StatusServiceUnavailable, failedResponseReader{}, nil)
	doc, err := LoadURLWithOptions(context.Background(), "https://example.invalid/page", LoadOptions{Client: client})
	if doc != nil {
		t.Fatal("document is non-nil on error")
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *HTTPStatusError", err)
	}
	if statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusServiceUnavailable)
	}
	if statusErr.URL != "https://example.invalid/page" {
		t.Fatalf("URL = %q, want the request URL", statusErr.URL)
	}
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsRedirectToHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/missing", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, optionsTestDocument)
	}))
	defer server.Close()

	doc, err := LoadURLWithOptions(context.Background(), server.URL+"/redirect", LoadOptions{})
	if doc != nil {
		t.Fatal("document is non-nil on error")
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *HTTPStatusError", err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusNotFound)
	}
	if statusErr.URL != server.URL+"/missing" {
		t.Fatalf("URL = %q, want final redirect URL %q", statusErr.URL, server.URL+"/missing")
	}
}

type contextBlockingBody struct {
	ctx     context.Context
	started chan struct{}
	once    sync.Once
	closes  int
}

func (b *contextBlockingBody) Read([]byte) (int, error) {
	b.once.Do(func() {
		close(b.started)
	})
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextBlockingBody) Close() error {
	b.closes++
	return nil
}

type loadOptionsResult struct {
	doc *html.Node
	err error
}

func waitForLoadOptionsResult(t *testing.T, ch <-chan loadOptionsResult) loadOptionsResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("LoadURLWithOptions did not return promptly")
		return loadOptionsResult{}
	}
}

func TestLoadURLWithOptionsCancelDuringRequest(t *testing.T) {
	started := make(chan struct{})
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan loadOptionsResult, 1)
	go func() {
		doc, err := LoadURLWithOptions(ctx, "https://example.invalid/page", LoadOptions{Client: client})
		result <- loadOptionsResult{doc: doc, err: err}
	}()

	<-started
	cancel()
	got := waitForLoadOptionsResult(t, result)
	if got.doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", got.err)
	}
}

func TestLoadURLWithOptionsCancelDuringBodyRead(t *testing.T) {
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	tracked := &contextBlockingBody{ctx: context.Background(), started: started}
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		tracked.ctx = req.Context()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       tracked,
			Request:    req,
		}, nil
	})}
	result := make(chan loadOptionsResult, 1)
	go func() {
		doc, err := LoadURLWithOptions(ctx, "https://example.invalid/page", LoadOptions{Client: client})
		result <- loadOptionsResult{doc: doc, err: err}
	}()

	<-started
	cancel()
	got := waitForLoadOptionsResult(t, result)
	if got.doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", got.err)
	}
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsDeadlineDuringBodyRead(t *testing.T) {
	started := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	tracked := &contextBlockingBody{ctx: context.Background(), started: started}
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		tracked.ctx = req.Context()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       tracked,
			Request:    req,
		}, nil
	})}
	result := make(chan loadOptionsResult, 1)
	go func() {
		doc, err := LoadURLWithOptions(ctx, "https://example.invalid/page", LoadOptions{Client: client})
		result <- loadOptionsResult{doc: doc, err: err}
	}()

	<-started
	got := waitForLoadOptionsResult(t, result)
	if got.doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", got.err)
	}
	if tracked.closes != 1 {
		t.Fatalf("response body Close calls = %d, want 1", tracked.closes)
	}
}

func TestLoadURLWithOptionsSharedClientLimitsAreIndependent(t *testing.T) {
	small := []byte(`<html><head><title>small</title></head><body></body></html>`)
	large := []byte(`<html><head><title>large</title></head><body>` + strings.Repeat("x", 4096) + `</body></html>`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/small":
			fmt.Fprint(w, string(small))
		case "/large":
			fmt.Fprint(w, string(large))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	const requests = 12
	var wg sync.WaitGroup
	errs := make(chan error, requests*4)
	for i := 0; i < requests; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			doc, err := LoadURLWithOptions(context.Background(), server.URL+"/small", LoadOptions{
				Client:           client,
				MaxResponseBytes: int64(len(small)),
				MaxDocumentBytes: int64(len(small)),
			})
			if err != nil {
				errs <- fmt.Errorf("small request: %w", err)
				return
			}
			if title := InnerText(FindOne(doc, "//title")); title != "small" {
				errs <- fmt.Errorf("small title = %q", title)
			}
		}()
		go func() {
			defer wg.Done()
			doc, err := LoadURLWithOptions(context.Background(), server.URL+"/large", LoadOptions{
				Client:           client,
				MaxResponseBytes: int64(len(small)),
			})
			if !errors.Is(err, ErrResponseTooLarge) {
				errs <- fmt.Errorf("large request error = %v, want ErrResponseTooLarge", err)
			}
			if doc != nil {
				errs <- errors.New("large request returned a document")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestLoadURLWithOptionsPreservesClientSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "present", Path: "/"})
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "present" {
				http.Error(w, "missing cookie", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>cookie page</title></head><body>ok</body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var redirects int32
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			atomic.AddInt32(&redirects, 1)
			return nil
		},
	}
	doc, err := LoadURLWithOptions(context.Background(), server.URL+"/start", LoadOptions{Client: client})
	if err != nil {
		t.Fatalf("LoadURLWithOptions() error = %v", err)
	}
	assertDocumentTitle(t, doc, "cookie page")
	if got := atomic.LoadInt32(&redirects); got != 1 {
		t.Fatalf("CheckRedirect calls = %d, want 1", got)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookies := jar.Cookies(serverURL)
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Value != "present" {
		t.Fatalf("Jar cookies = %#v, want session=present", cookies)
	}
	if client.Transport != transport || client.Timeout != 2*time.Second || client.Jar != jar {
		t.Fatal("LoadURLWithOptions modified the shared client")
	}
}

func TestLoadURLWithOptionsClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
			fmt.Fprint(w, optionsTestDocument)
		}
	}))
	defer server.Close()

	client := &http.Client{Timeout: 40 * time.Millisecond}
	start := time.Now()
	doc, err := LoadURLWithOptions(context.Background(), server.URL, LoadOptions{Client: client})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the client timeout to trigger")
	}
	if doc != nil {
		t.Fatal("document is non-nil on error")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("request took %v, client timeout was not honored", elapsed)
	}
}

func TestLegacyLoadURLWithClientBehaviorUnchanged(t *testing.T) {
	body := `<html><head><title>legacy</title></head><body>` + strings.Repeat("x", 4096) + `</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	doc, err := LoadURLWithClient(server.URL, client)
	if err != nil {
		t.Fatalf("LoadURLWithClient() error = %v", err)
	}
	assertDocumentTitle(t, doc, "legacy")

	doc, err = LoadURL(server.URL)
	if err != nil {
		t.Fatalf("LoadURL() error = %v", err)
	}
	assertDocumentTitle(t, doc, "legacy")
}
