package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxPeekBody caps how much of a request body is read to find its JSON-RPC id.
const maxPeekBody = 1 << 20

// rpcServerError is the JSON-RPC implementation-defined server error code used
// for requests rejected before they reach the MCP server (rate limit, access).
const rpcServerError = -32000

// jsonRPCErrors turns the plain JSON error that the rate limiter or access
// middleware writes into a JSON-RPC error response with the same HTTP status,
// headers, and message. MCP clients then show "rate limit exceeded" or
// "missing access grant" as a tool error instead of a transport failure.
// Responses from the MCP handler itself pass through untouched.
func jsonRPCErrors(c *gin.Context) {
	w := &rpcErrorWriter{ResponseWriter: c.Writer}
	c.Writer = w
	c.Set(mcpReachedKey, w)
	defer func() { c.Writer = w.ResponseWriter }()
	c.Next()
	if w.buf == nil {
		return
	}
	body := rpcErrorBody(peekRPCID(c.Request), w.status, w.buf.Bytes())
	w.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.ResponseWriter.Header().Del("Content-Length")
	_, _ = w.ResponseWriter.Write(body)
}

const mcpReachedKey = "tsflow.mcp.reached"

// markReached stops the error rewrite once the MCP handler runs.
func markReached(c *gin.Context) {
	if v, ok := c.Get(mcpReachedKey); ok {
		if w, ok := v.(*rpcErrorWriter); ok {
			w.reached = true
		}
	}
}

type rpcErrorWriter struct {
	gin.ResponseWriter
	reached bool
	status  int
	buf     *bytes.Buffer
}

func (w *rpcErrorWriter) capture() bool {
	return !w.reached && w.ResponseWriter.Status() >= http.StatusBadRequest
}

func (w *rpcErrorWriter) Write(b []byte) (int, error) {
	if w.capture() {
		if w.buf == nil {
			w.buf = &bytes.Buffer{}
			w.status = w.ResponseWriter.Status()
		}
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *rpcErrorWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// peekRPCID returns the id of a single JSON-RPC request, or null. The body
// is restored so nothing downstream sees it consumed.
func peekRPCID(r *http.Request) json.RawMessage {
	null := json.RawMessage("null")
	if r == nil || r.Body == nil || r.Method != http.MethodPost {
		return null
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxPeekBody))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(data), r.Body))
	if err != nil {
		return null
	}
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(data, &msg) != nil || len(msg.ID) == 0 {
		return null
	}
	switch msg.ID[0] {
	case '"', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return msg.ID
	}
	return null
}

func rpcErrorBody(id json.RawMessage, status int, plain []byte) []byte {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(plain, &parsed)
	message := parsed.Error
	switch {
	case message == "":
		message = http.StatusText(status)
	case parsed.Message != "":
		message += ": " + parsed.Message
	}
	out, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcError        `json:"error"`
	}{"2.0", id, rpcError{Code: rpcServerError, Message: message, Data: rpcErrorData{HTTPStatus: status}}})
	return out
}

type rpcError struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Data    rpcErrorData `json:"data"`
}

type rpcErrorData struct {
	HTTPStatus int `json:"httpStatus"`
}
