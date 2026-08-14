package modelapi

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"sync"
)

// filterEmptySSEFrames drops SSE frames that have no data payload.
// Some OpenAI-compatible proxies (including local cliproxy) terminate a
// Responses stream with an extra blank event. openai-go's decoder then
// json.Unmarshals empty bytes and fails with "unexpected end of JSON input".
func filterEmptySSEFrames(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return body
	}
	pr, pw := io.Pipe()
	go func() {
		err := copySSEDroppingEmptyFrames(pw, body)
		_ = body.Close()
		_ = pw.CloseWithError(err)
	}()
	return pr
}

func copySSEDroppingEmptyFrames(dst io.Writer, src io.Reader) error {
	scn := bufio.NewScanner(src)
	scn.Buffer(nil, 8<<20)
	var frame []string
	hasData := false
	flush := func() error {
		if !hasData {
			frame = frame[:0]
			return nil
		}
		for _, line := range frame {
			if _, err := io.WriteString(dst, line); err != nil {
				return err
			}
			if _, err := io.WriteString(dst, "\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(dst, "\n"); err != nil {
			return err
		}
		frame = frame[:0]
		hasData = false
		return nil
	}
	for scn.Scan() {
		line := scn.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		frame = append(frame, line)
		if strings.HasPrefix(line, "data:") && strings.TrimSpace(strings.TrimPrefix(line, "data:")) != "" &&
			strings.TrimSpace(strings.TrimPrefix(line, "data:")) != "[DONE]" {
			hasData = true
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return scn.Err()
}

func wrapResponsesSSEBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "text/event-stream") {
		return
	}
	resp.Body = &sseFilterCloser{ReadCloser: filterEmptySSEFrames(resp.Body)}
}

type sseFilterCloser struct {
	io.ReadCloser
	once sync.Once
}

func (c *sseFilterCloser) Close() error {
	var err error
	c.once.Do(func() { err = c.ReadCloser.Close() })
	return err
}
