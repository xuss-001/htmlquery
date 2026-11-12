# htmlquery

[![Build Status](https://github.com/antchfx/htmlquery/actions/workflows/testing.yml/badge.svg)](https://github.com/antchfx/htmlquery/actions/workflows/testing.yml)
[![GoDoc](https://godoc.org/github.com/antchfx/htmlquery?status.svg)](https://godoc.org/github.com/antchfx/htmlquery)
[![Go Report Card](https://goreportcard.com/badge/github.com/antchfx/htmlquery)](https://goreportcard.com/report/github.com/antchfx/htmlquery)

# Overview

`htmlquery` is an XPath query package for HTML, lets you extract data or evaluate from HTML documents by an XPath expression.

`htmlquery` built-in the query object caching feature based on [LRU](https://godoc.org/github.com/golang/groupcache/lru), this feature will caching the recently used XPATH query string. Enable query caching can avoid re-compile XPath expression each query.

You can visit this page to learn about the supported XPath(1.0/2.0) syntax. https://github.com/antchfx/xpath

# XPath query packages for Go

| Name                                              | Description                               |
| ------------------------------------------------- | ----------------------------------------- |
| [htmlquery](https://github.com/antchfx/htmlquery) | XPath query package for the HTML document |
| [xmlquery](https://github.com/antchfx/xmlquery)   | XPath query package for the XML document  |
| [jsonquery](https://github.com/antchfx/jsonquery) | XPath query package for the JSON document |

# Installation

```
go get github.com/antchfx/htmlquery
```

# Getting Started

#### Query, returns matched elements or error.

```go
nodes, err := htmlquery.QueryAll(doc, "//a")
if err != nil {
	panic(`not a valid XPath expression.`)
}
```

#### Load HTML document from URL.

```go
doc, err := htmlquery.LoadURL("http://example.com/")
```

#### Load an HTML document with cancellation and size limits.

`LoadURLWithOptions` accepts a context and per-call limits without changing
the behavior of the existing `LoadURL` and `LoadURLWithClient` functions.

```go
options := htmlquery.LoadOptions{
	Client:           httpClient, // nil uses http.DefaultClient
	MaxResponseBytes: 10 << 20,   // bytes read from the response Body
	MaxDocumentBytes: 20 << 20,   // decoded UTF-8 bytes passed to the parser
}

doc, err := htmlquery.LoadURLWithOptions(ctx, url, options)
switch {
case errors.Is(err, htmlquery.ErrResponseTooLarge):
	// Skip the HTTP response body limit violation.
case errors.Is(err, htmlquery.ErrDocumentTooLarge):
	// Skip the decoded document limit violation.
case err != nil:
	var statusErr *htmlquery.HTTPStatusError
	if errors.As(err, &statusErr) {
		log.Printf("HTTP %d at %s", statusErr.StatusCode, statusErr.URL)
	}
}
```

`MaxResponseBytes` is checked while the response body is read, before
htmlquery decompresses gzip or zlib deflate data and before charset conversion.
If the client's `Transport` transparently decompresses the response, the limit
applies to the decompressed bytes supplied by that `Transport`. `MaxDocumentBytes`
is checked while the decoded UTF-8 document is passed to the HTML parser.
Zero disables the corresponding limit. Exact-limit data is accepted; one extra
byte returns the corresponding sentinel error.

The context controls both the request and response body reads, so cancellation
and deadlines are reported through `errors.Is(err, context.Canceled)` and
`errors.Is(err, context.DeadlineExceeded)`. Only final HTTP status codes from
200 through 299 are parsed. Other statuses return `*htmlquery.HTTPStatusError`
before the body is decoded or parsed.

The supplied `http.Client` is never modified, so its `Transport`, `Timeout`,
`CheckRedirect`, and `Jar` settings remain effective and the same client can be
used concurrently with different limits. The library always closes the
response body it consumes.

For a runnable, local-only example that uses XPath and continues after an
oversized page is rejected, run:

```sh
go run ./examples/load_url_with_options
```

#### Load HTML from document.

```go
filePath := "/home/user/sample.html"
doc, err := htmlquery.LoadDoc(filePath)
```

#### Load HTML document from string.

```go
s := `<html>....</html>`
doc, err := htmlquery.Parse(strings.NewReader(s))
```

#### Find all A elements.

```go
list := htmlquery.Find(doc, "//a")
```

#### Find all A elements that have `href` attribute.

```go
list := htmlquery.Find(doc, "//a[@href]")
```

#### Find all A elements with `href` attribute and only return `href` value.

```go
list := htmlquery.Find(doc, "//a/@href")
for _ , n := range list{
	fmt.Println(htmlquery.InnerText(n)) // output @href value
}
```

### Find the third A element.

```go
a := htmlquery.FindOne(doc, "//a[3]")
```

### Find children element (img) under A `href` and print the source

```go
a := htmlquery.FindOne(doc, "//a")
img := htmlquery.FindOne(a, "//img")
fmt.Prinln(htmlquery.SelectAttr(img, "src")) // output @src value
```

#### Evaluate the number of all IMG element.

```go
expr, _ := xpath.Compile("count(//img)")
v := expr.Evaluate(htmlquery.CreateXPathNavigator(doc)).(float64)
fmt.Printf("total count is %f", v)
```

# Quick Starts

```go
func main() {
	doc, err := htmlquery.LoadURL("https://www.bing.com/search?q=golang")
	if err != nil {
		panic(err)
	}
	// Find all news item.
	list, err := htmlquery.QueryAll(doc, "//ol/li")
	if err != nil {
		panic(err)
	}
	for i, n := range list {
		a := htmlquery.FindOne(n, "//a")
		if a != nil {
		    fmt.Printf("%d %s(%s)\n", i, htmlquery.InnerText(a), htmlquery.SelectAttr(a, "href"))
		}
	}
}
```

# FAQ

#### `Find()` vs `QueryAll()`, which is better?

`Find` and `QueryAll` both do the same things, searches all of matched html nodes.
The `Find` will panics if you give an error XPath query, but `QueryAll` will return an error for you.

#### Can I save my query expression object for the next query?

Yes, you can. We offer the `QuerySelector` and `QuerySelectorAll` methods, It will accept your query expression object.

Cache a query expression object(or reused) will avoid re-compile XPath query expression, improve your query performance.

#### XPath query object cache performance

```
goos: windows
goarch: amd64
pkg: github.com/antchfx/htmlquery
BenchmarkSelectorCache-4                20000000                55.2 ns/op
BenchmarkDisableSelectorCache-4           500000              3162 ns/op
```

#### How to disable caching?

```
htmlquery.DisableSelectorCache = true
```

# Questions

Please let me know if you have any questions.
