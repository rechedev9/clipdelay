// Command clipdelay runs the ClipDelay relay: point OBS at rtmp://127.0.0.1:1935/live and change
// the delay from stdin while the stream stays up.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rechedev9/clipdelay/internal/relay"
)

const usage = "commands: match|partida (match delay), end|fin (live once viewers see the end), " +
	"<seconds> or a duration (30, 90s, 2m), live, status, json, quit"

func main() {
	listen := flag.String("listen", "127.0.0.1:1935", "address OBS publishes to")
	ingestKey := flag.String("ingest-key", "", "stream key OBS must use (empty accepts any)")
	initial := flag.Duration("delay", 0, "delay at start")
	matchDelay := flag.Duration("match-delay", 3*time.Minute, "delay the partida command sets")
	retain := flag.Duration("retain", 5*time.Minute, "history kept in memory; the largest delay")
	maxMB := flag.Int64("max-mb", 512, "hard cap on buffered stream data, in MB")
	control := flag.Bool("control", false, "run under the OBS plugin: exit once stdin closes and the output drained")
	flag.Parse()

	// Buffered video is pointer-free bytes, so collecting more often costs little CPU and keeps the
	// process near the buffered size instead of the default 2x.
	debug.SetGCPercent(25)
	// The RTMP library logs to the global logger, sometimes with connection details.
	logrus.SetOutput(io.Discard)
	if *matchDelay > *retain {
		log.Fatalf("-match-delay %s needs -retain of at least that much", *matchDelay)
	}
	upstream := os.Getenv("CLIPDELAY_UPSTREAM")
	if upstream == "" {
		log.Fatal("set CLIPDELAY_UPSTREAM to the platform URL with the stream key, e.g. rtmp://live.twitch.tv/app/<key>")
	}
	rl, err := relay.New(relay.Config{
		Upstream: upstream, IngestKey: *ingestKey, Delay: *initial,
		Retain: *retain, MaxBytes: *maxMB << 20, Logf: log.Printf,
	})
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("OBS: rtmp://%s/live  (%s)", ln.Addr(), usage)
	c := console{rl: rl, matchDelay: *matchDelay, control: *control, stop: stop}
	go c.run()
	if err := rl.Serve(ctx, ln); err != nil {
		log.Fatal(err)
	}
}

// console reads commands from stdin: a person typing, or the OBS plugin.
type console struct {
	rl         *relay.Relay
	matchDelay time.Duration
	control    bool
	stop       func()
}

func (c console) run() {
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		switch line {
		case "":
		case "quit", "exit":
			c.stop()
			return
		case "status":
			printStatus(c.rl.Status())
		case "json":
			printJSON(c.rl.Status())
		case "live":
			report(c.rl.SetDelay(0))
		case "partida", "match":
			report(c.rl.SetDelay(c.matchDelay))
		case "fin", "end":
			c.rl.EndMatch()
			report(nil)
		default:
			d, err := parseDelay(line)
			if err != nil {
				fmt.Println(usage)
				continue
			}
			report(c.rl.SetDelay(d))
		}
	}
	if c.control {
		c.stopWhenDrained()
	}
}

// stopWhenDrained runs after OBS closed: the viewers still get the delayed tail before the exit.
func (c console) stopWhenDrained() {
	for {
		s := c.rl.Status()
		if !s.Ingesting && s.Upstream.State == "" {
			c.stop()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func parseDelay(s string) (time.Duration, error) {
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("not a delay")
	}
	return d, nil
}

func report(err error) {
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("ok")
}

func printStatus(s relay.Status) {
	r := s.Reader
	fmt.Printf("phase=%s target=%s applied=%s ready_in=%s lag=%s jumps=%d\n",
		r.Phase, r.Target, r.Applied, r.ReadyIn.Round(time.Second), r.Lag.Round(time.Millisecond), r.Jumps)
	fmt.Printf("ingest=%t buffer=%s %dMB upstream=%s reconnects=%d sent=%dMB\n",
		s.Ingesting, s.Buffer.Covered.Round(time.Second), s.Buffer.Bytes>>20,
		s.Upstream.State, s.Upstream.Reconnects, s.Upstream.BytesOut>>20)
	if s.Upstream.LastError != "" {
		fmt.Println("last error:", s.Upstream.LastError)
	}
}

type jsonStatus struct {
	Phase      string `json:"phase"`
	TargetMs   int64  `json:"target_ms"`
	AppliedMs  int64  `json:"applied_ms"`
	ReadyInMs  int64  `json:"ready_in_ms"`
	Ingesting  bool   `json:"ingesting"`
	BufferMB   int64  `json:"buffer_mb"`
	Upstream   string `json:"upstream"`
	Reconnects int    `json:"reconnects"`
	LastError  string `json:"last_error"`
}

// printJSON writes the status as one line for the OBS plugin.
func printJSON(s relay.Status) {
	line, _ := json.Marshal(jsonStatus{
		Phase: s.Reader.Phase, TargetMs: s.Reader.Target.Milliseconds(), AppliedMs: s.Reader.Applied.Milliseconds(),
		ReadyInMs: s.Reader.ReadyIn.Milliseconds(), Ingesting: s.Ingesting, BufferMB: s.Buffer.Bytes >> 20,
		Upstream: s.Upstream.State, Reconnects: s.Upstream.Reconnects, LastError: s.Upstream.LastError,
	})
	fmt.Println(string(line))
}
