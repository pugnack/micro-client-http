package http_test

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

func printHTTPRequest(r *http.Request) {
	var b strings.Builder

	_, _ = fmt.Fprintf(&b, "\n%s %s %s\n", r.Method, r.URL.RequestURI(), r.Proto)

	for name, values := range r.Header {
		for _, value := range values {
			_, _ = fmt.Fprintf(&b, "%s: %s\n", name, value)
		}
	}

	b.WriteString("\n")

	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		b.Write(body)
	}

	log.Print(b.String())
}
