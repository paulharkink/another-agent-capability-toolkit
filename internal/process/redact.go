package process

import (
	"bytes"
	"sort"
	"strings"
)

// Redactor retains only enough trailing bytes to recognize a secret split by a
// process stderr read. Flush must be called once the process has completed.
type Redactor struct {
	values  []string
	pending []byte
	maximum int
	emit    func([]byte)
}

func NewRedactor(values []string, emit func([]byte)) *Redactor {
	r := &Redactor{maximum: 1, emit: emit}
	for _, v := range values {
		if v != "" {
			r.values = append(r.values, v)
			if len(v) > r.maximum {
				r.maximum = len(v)
			}
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}
func (r *Redactor) Write(p []byte) {
	r.pending = append(r.pending, p...)
	var out bytes.Buffer
	for len(r.pending) >= r.maximum {
		matched := false
		for _, v := range r.values {
			if bytes.HasPrefix(r.pending, []byte(v)) {
				out.WriteString("[redacted]")
				r.pending = r.pending[len(v):]
				matched = true
				break
			}
		}
		if !matched {
			out.WriteByte(r.pending[0])
			r.pending = r.pending[1:]
		}
	}
	if out.Len() > 0 && r.emit != nil {
		r.emit(out.Bytes())
	}
}
func (r *Redactor) Flush() {
	s := string(r.pending)
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	r.pending = nil
	if s != "" && r.emit != nil {
		r.emit([]byte(s))
	}
}
