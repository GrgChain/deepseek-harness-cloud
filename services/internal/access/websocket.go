package access

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Read and forward the original frames without buffering a message. Compression
// is not negotiated. Both directions have a 10MiB message bound, including
// fragmented messages; control frames never refresh Portal idle time.
type frameReader struct {
	reader             io.Reader
	masked             bool
	activity           *atomic.Bool
	header             []byte
	remaining, message uint64
	fragmented, data   bool
}

func newFrameReader(reader io.Reader, masked bool, activity *atomic.Bool) *frameReader {
	return &frameReader{reader: reader, masked: masked, activity: activity}
}

var errFrame = errors.New("invalid or oversized WebSocket frame")

func (f *frameReader) next() error {
	header := make([]byte, 2, 14)
	if _, err := io.ReadFull(f.reader, header); err != nil {
		return err
	}
	fin, opcode := header[0]&0x80 != 0, header[0]&15
	if header[0]&0x70 != 0 || (header[1]&0x80 != 0) != f.masked {
		return errFrame
	}
	n := uint64(header[1] & 127)
	if n == 126 {
		header = append(header, 0, 0)
		if _, err := io.ReadFull(f.reader, header[2:]); err != nil {
			return err
		}
		n = uint64(binary.BigEndian.Uint16(header[2:]))
		if n < 126 {
			return errFrame
		}
	}
	if n == 127 {
		header = append(header, make([]byte, 8)...)
		if _, err := io.ReadFull(f.reader, header[2:]); err != nil {
			return err
		}
		n = binary.BigEndian.Uint64(header[2:])
		if n < 65536 || n>>63 != 0 {
			return errFrame
		}
	}
	f.data = false
	switch opcode {
	case 0:
		if !f.fragmented {
			return errFrame
		}
		f.data = true
		f.message += n
		if fin {
			f.fragmented = false
		}
	case 1, 2:
		if f.fragmented {
			return errFrame
		}
		f.data = true
		f.message = n
		f.fragmented = !fin
	case 8, 9, 10:
		if !fin || n > 125 {
			return errFrame
		}
	default:
		return errFrame
	}
	if n > 10<<20 || (f.data && f.message > 10<<20) {
		return errFrame
	}
	if f.masked {
		offset := len(header)
		header = append(header, 0, 0, 0, 0)
		if _, err := io.ReadFull(f.reader, header[offset:]); err != nil {
			return err
		}
	}
	f.header = header
	f.remaining = n
	return nil
}
func (f *frameReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(f.header) == 0 && f.remaining == 0 {
		if err := f.next(); err != nil {
			return 0, err
		}
	}
	if len(f.header) > 0 {
		n := copy(p, f.header)
		f.header = f.header[n:]
		return n, nil
	}
	if uint64(len(p)) > f.remaining {
		p = p[:int(f.remaining)]
	}
	n, err := f.reader.Read(p)
	f.remaining -= uint64(n)
	if n > 0 && f.data && f.activity != nil {
		f.activity.Store(true)
	}
	return n, err
}

type socketConn struct {
	net.Conn
	reader *frameReader
}

func (c *socketConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type socketWriter struct {
	http.ResponseWriter
	activity *atomic.Bool
}

func (w *socketWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *socketWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return &socketConn{conn, newFrameReader(buffer.Reader, true, w.activity)}, buffer, nil
}

type socketBody struct {
	io.ReadWriteCloser
	reader *frameReader
}

func (b *socketBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
