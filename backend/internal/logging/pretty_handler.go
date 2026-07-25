package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type prettyHandler struct {
	writer io.Writer
	level  slog.Leveler
	mutex  *sync.Mutex
	attrs  []boundAttr
	groups []string
}

type boundAttr struct {
	attr   slog.Attr
	groups []string
}

type prettyField struct {
	key   string
	value slog.Value
}

func newPrettyHandler(writer io.Writer, level slog.Leveler) slog.Handler {
	return &prettyHandler{writer: writer, level: level, mutex: &sync.Mutex{}}
}

func (handler *prettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level.Level()
}

func (handler *prettyHandler) Handle(_ context.Context, record slog.Record) error {
	fields := make([]prettyField, 0, len(handler.attrs)+record.NumAttrs()+1)
	for _, bound := range handler.attrs {
		appendPrettyAttr(&fields, bound.groups, bound.attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendPrettyAttr(&fields, handler.groups, attr)
		return true
	})

	component := ""
	visible := fields[:0]
	for _, field := range fields {
		if field.key == "component" {
			component = field.value.String()
			continue
		}
		visible = append(visible, field)
	}
	fields = visible
	if source := shortSource(record.PC); source != "" {
		fields = append(fields, prettyField{key: "source", value: slog.StringValue(source)})
	}

	var output bytes.Buffer
	output.WriteString(record.Time.Format("2006-01-02 15:04:05.000Z07:00"))
	output.WriteByte(' ')
	output.WriteString(fmt.Sprintf("%-5s", strings.ToUpper(record.Level.String())))
	if component != "" {
		output.WriteString(" [")
		output.WriteString(component)
		output.WriteByte(']')
	}
	output.WriteByte(' ')
	output.WriteString(strings.ReplaceAll(record.Message, "\n", `\n`))

	multilineFields := make([]prettyField, 0, 1)
	for _, field := range fields {
		value := prettyValue(field.value)
		if strings.Contains(value, "\n") && (field.key == "stack" || strings.HasSuffix(field.key, ".stack")) {
			multilineFields = append(multilineFields, field)
			continue
		}
		output.WriteByte(' ')
		output.WriteString(field.key)
		output.WriteByte('=')
		output.WriteString(prettyInlineValue(value))
	}
	output.WriteByte('\n')
	for _, field := range multilineFields {
		value := prettyValue(field.value)
		output.WriteString("  ")
		output.WriteString(field.key)
		output.WriteString(":\n    ")
		output.WriteString(strings.ReplaceAll(strings.TrimSuffix(value, "\n"), "\n", "\n    "))
		output.WriteByte('\n')
	}

	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	_, err := handler.writer.Write(output.Bytes())
	return err
}

func prettyInlineValue(value string) string {
	if value == "" || strings.ContainsAny(value, " \t\r\n=\"") {
		return strconv.Quote(value)
	}
	return value
}

func (handler *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := handler.clone()
	for _, attr := range attrs {
		clone.attrs = append(clone.attrs, boundAttr{attr: attr, groups: append([]string(nil), handler.groups...)})
	}
	return clone
}

func (handler *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	clone := handler.clone()
	clone.groups = append(clone.groups, name)
	return clone
}

func (handler *prettyHandler) clone() *prettyHandler {
	clone := *handler
	clone.attrs = append([]boundAttr(nil), handler.attrs...)
	clone.groups = append([]string(nil), handler.groups...)
	return &clone
}

func appendPrettyAttr(fields *[]prettyField, groups []string, attr slog.Attr) {
	value := attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if value.Kind() == slog.KindGroup {
		if attr.Key != "" {
			groups = append(append([]string(nil), groups...), attr.Key)
		}
		for _, child := range value.Group() {
			appendPrettyAttr(fields, groups, child)
		}
		return
	}
	keyParts := append([]string(nil), groups...)
	if attr.Key != "" {
		keyParts = append(keyParts, attr.Key)
	}
	*fields = append(*fields, prettyField{key: strings.Join(keyParts, "."), value: value})
}

func prettyValue(value slog.Value) string {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindBool:
		return strconv.FormatBool(value.Bool())
	case slog.KindInt64:
		return strconv.FormatInt(value.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(value.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(value.Float64(), 'g', -1, 64)
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return err.Error()
		}
		if encoded, err := json.Marshal(value.Any()); err == nil {
			return string(encoded)
		}
		return fmt.Sprint(value.Any())
	default:
		return value.String()
	}
}

func shortSource(programCounter uintptr) string {
	if programCounter == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{programCounter}).Next()
	file := filepath.ToSlash(frame.File)
	if marker := strings.LastIndex(file, "/backend/"); marker >= 0 {
		file = file[marker+len("/backend/"):]
	} else {
		file = filepath.Base(file)
	}
	return fmt.Sprintf("%s:%d", file, frame.Line)
}
