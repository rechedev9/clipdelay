package relay

import (
	"bytes"
	"errors"
	"io"
	"net"

	rtmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/rechedev9/cliphub/clipdelay/internal/delay"
	"github.com/rechedev9/cliphub/clipdelay/internal/flv"
)

var errNotPublishing = errors.New("relay: media before publish")

// ingestHandler receives one OBS connection and pushes its tags into the delay buffer untouched.
type ingestHandler struct {
	rtmp.DefaultHandler
	relay   *Relay
	conn    net.Conn
	buf     *delay.Buffer
	claimed bool
	started bool
}

func (h *ingestHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	buf, err := h.relay.beginIngest(cmd.PublishingName)
	if err != nil {
		return err
	}
	h.buf, h.claimed = buf, true
	return nil
}

func (h *ingestHandler) OnSetDataFrame(ts uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	return h.push(flv.Frame{Type: flv.TypeScript, Time: ts, Data: bytes.Clone(data.Payload)})
}

func (h *ingestHandler) OnAudio(ts uint32, payload io.Reader) error {
	return h.pushReader(flv.Frame{Type: flv.TypeAudio, Time: ts}, payload)
}

func (h *ingestHandler) OnVideo(ts uint32, payload io.Reader) error {
	return h.pushReader(flv.Frame{Type: flv.TypeVideo, Time: ts}, payload)
}

// pushReader copies the payload: the library reuses its buffer after the callback returns.
func (h *ingestHandler) pushReader(f flv.Frame, payload io.Reader) error {
	data, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	f.Data = data
	return h.push(f)
}

func (h *ingestHandler) push(f flv.Frame) error {
	if h.buf == nil {
		return errNotPublishing
	}
	f.Flags = flv.Classify(f.Type, f.Data)
	if !h.started {
		f.Flags |= flv.FlagSessionStart
		h.started = true
	}
	f.Recv = h.relay.now()
	return h.buf.Push(f)
}

func (h *ingestHandler) OnClose() {
	h.relay.endIngest(h.conn, h.claimed)
}
