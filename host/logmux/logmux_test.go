package logmux

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	. "github.com/flynn/flynn/Godeps/_workspace/src/github.com/flynn/go-check"
	"github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/discoverd/testutil"
	"github.com/flynn/flynn/discoverd/testutil/etcdrunner"
	"github.com/flynn/flynn/pkg/syslog/rfc5424"
	"github.com/flynn/flynn/pkg/syslog/rfc6587"
)

// Hook gocheck up to the "go test" runner
func TestLogMux(t *testing.T) { TestingT(t) }

type S struct {
	discd   *discoverd.Client
	cleanup func()
}

var _ = Suite(&S{})

func (s *S) SetUpSuite(c *C) {
	etcdAddr, killEtcd := etcdrunner.RunEtcdServer(c)
	discd, killDiscoverd := testutil.BootDiscoverd(c, "", etcdAddr)

	s.discd = discd
	s.cleanup = func() {
		killDiscoverd()
		killEtcd()
	}
}

func (s *S) TearDownSuite(c *C) {
	s.cleanup()
}

func (s *S) TestSetup(c *C) {
	if _, err := New(s.discd, 100); err == nil {
		c.Fatal("logmux setup before logaggregator leader available")
	}

	l, err := net.Listen("tcp", ":0")
	if err != nil {
		c.Fatal(err)
	}
	defer l.Close()

	addr := l.Addr().String()
	hb, err := s.discd.AddServiceAndRegister("logaggregator", addr)
	if err != nil {
		c.Fatal(err)
	}
	defer hb.Close()

	if _, err := New(s.discd, 100); err != nil {
		c.Errorf("logmux setup error: %s", err)
	}
}

func (s *S) TestLogMux(c *C) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		c.Fatal(err)
	}
	defer l.Close()

	addr := l.Addr().String()
	hb, err := s.discd.AddServiceAndRegister("logaggregator", addr)
	if err != nil {
		c.Fatal(err)
	}
	defer hb.Close()

	mu := sync.Mutex{}
	srvDone := make(chan struct{})
	msgCount := 0
	handler := func(msg *rfc5424.Message) {
		mu.Lock()
		defer mu.Unlock()

		msgCount += 1
		if msgCount == 10000 {
			close(srvDone)
		}
	}

	go runServer(l, handler)

	lm, err := New(s.discd, 10000)
	if err != nil {
		c.Fatal(err)
	}

	config := Config{
		AppName: "test",
		IP:      "1.2.3.4",
		JobType: "worker",
		JobID:   "567",
	}

	for i := 0; i < 100; i++ {
		pr, pw := io.Pipe()
		lm.Follow(pr, i, config)

		go func() {
			defer pw.Close()
			for j := 0; j < 100; j++ {
				fmt.Fprintf(pw, "test log entry %d\n", j)
			}
		}()
	}

	lm.Close()
	<-srvDone
}

func (s *S) TestLIFOBuffer(c *C) {
	n := 100
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		c.Fatal(err)
	}
	defer l.Close()

	mu := sync.Mutex{}
	srvDone := make(chan struct{})
	msgCount := 0
	handler := func(msg *rfc5424.Message) {
		mu.Lock()
		defer mu.Unlock()

		if !bytes.Equal(msg.Msg, []byte("retained")) {
			close(srvDone)
			c.Assert(msg.Msg, DeepEquals, []byte("retained"))
		}

		msgCount += 1
		if msgCount == n {
			close(srvDone)
		}
	}

	go runServer(l, handler)

	addr := l.Addr().String()
	hb, err := s.discd.AddServiceAndRegister("logaggregator", addr)
	if err != nil {
		c.Fatal(err)
	}

	// pause drainer so that messages buffer
	pausec := make(chan struct{})
	drainHook = func() { <-pausec }

	lm, err := New(s.discd, n)
	if err != nil {
		c.Fatal(err)
	}

	hb.Close()

	config := Config{
		AppName: "test",
		IP:      "1.2.3.4",
		JobType: "worker",
		JobID:   "567",
	}

	pr, pw := io.Pipe()
	lm.Follow(pr, 1, config)

	for i := 0; i < n; i++ {
		fmt.Fprintf(pw, "retained\n")
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(pw, "dropped\n")
	}
	pw.Close()

	close(pausec)
	<-srvDone
}

func runServer(l net.Listener, h func(*rfc5424.Message)) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		go func() {
			s := bufio.NewScanner(conn)
			s.Split(rfc6587.Split)

			for s.Scan() {
				msgCopy := make([]byte, len(s.Bytes()))
				copy(msgCopy, s.Bytes())

				msg, err := rfc5424.Parse(msgCopy)
				if err != nil {
					conn.Close()
					return
				}

				h(msg)
			}
			conn.Close()
		}()
	}
}