package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/flynn/flynn/pkg/ctxhelper"
	"github.com/flynn/flynn/pkg/httphelper"
	"github.com/flynn/flynn/pkg/syslog/rfc5424"

	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/julienschmidt/httprouter"
	"github.com/flynn/flynn/Godeps/_workspace/src/golang.org/x/net/context"
	"github.com/flynn/flynn/Godeps/_workspace/src/gopkg.in/inconshreveable/log15.v2"
)

func apiHandler(agg *Aggregator) http.Handler {
	api := aggregatorAPI{agg: agg}
	r := httprouter.New()

	r.GET("/log/:channel_id", httphelper.WrapHandler(api.GetLog))
	return httphelper.ContextInjector(
		"logaggregator-api",
		httphelper.NewRequestLogger(r),
	)
}

type aggregatorAPI struct {
	agg *Aggregator
}

func (a *aggregatorAPI) GetLog(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	params, _ := ctxhelper.ParamsFromContext(ctx)
	channelID := params.ByName("channel_id")
	vals := req.URL.Query()

	// TODO(bgentry): tail -f support

	lines := 0
	strLines := vals.Get("lines")
	if strLines != "" {
		var err error
		lines, err = strconv.Atoi(strLines)
		if err != nil || lines < 0 || lines > 10000 {
			respondWithError(w, "lines", "lines must be an integer between 0 and 10000")
			return
		}
	}

	// TODO(bgentry): sort here, or sort w/ heap in buffer...
	w.WriteHeader(200)
	messages := a.agg.ReadLastN(channelID, lines)
	enc := json.NewEncoder(w)
	for _, syslogMsg := range messages {
		if err := enc.Encode(NewMessageFromSyslog(syslogMsg)); err != nil {
			log15.Error("error writing msg", "err", err)
			return
		}
	}
}

// Message represents a single log message.
type Message struct {
	// Hostname is the host that the job was running on when this log message was
	// emitted.
	Hostname string `json:"hostname,omitempty"`
	// JobID is the ID of the job that emitted this log message.
	JobID string `json:"job_id,omitempty"`
	// ProcessType is the type of process that emitted this log message.
	ProcessType string `json:"process_type,omitempty"`
	// Source is the source of this log message, such as "app" or "router".
	Source string `json:"source,omitempty"`
	// Stream is the I/O stream that emitted this message, such as "stdout" or
	// "stderr".
	Stream string `json:"stream,omitempty"`
	// Timestamp is the time that this log line was emitted.
	Timestamp time.Time `json:"timestamp,omitempty"`
}

func NewMessageFromSyslog(m *rfc5424.Message) Message {
	processType, jobID := splitProcID(m.ProcID)
	return Message{
		Hostname:    string(m.Hostname),
		JobID:       jobID,
		ProcessType: processType,
		// TODO(bgentry): source is always "app" for now, could be router in future
		Source:    "app",
		Stream:    streamFromMessage(m),
		Timestamp: m.Timestamp,
	}
}

// TODO(bgentry): does this belong in the syslog package?
func splitProcID(procID []byte) (processType, jobID string) {
	split := bytes.Split(procID, []byte{'.'})
	if len(split) > 0 {
		processType = string(split[0])
	}
	if len(split) > 1 {
		jobID = string(split[1])
	}
	return
}

func streamFromMessage(m *rfc5424.Message) string {
	switch m.Severity {
	case 3:
		return "stderr"
	case 6:
		return "stdout"
	default:
		return "unknown"
	}
}

func respondWithError(w http.ResponseWriter, field, msg string) {
	detail, _ := json.Marshal(map[string]string{"field": field})
	httphelper.Error(w, httphelper.JSONError{
		Code:    httphelper.ValidationError,
		Message: msg,
		Detail:  detail,
	})
}
