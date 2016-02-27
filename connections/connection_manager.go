package connections

import (
	"errors"
	"fmt"
	"github.com/vivowares/eywa/Godeps/_workspace/src/github.com/gorilla/websocket"
	"github.com/vivowares/eywa/Godeps/_workspace/src/github.com/spaolacci/murmur3"
	. "github.com/vivowares/eywa/configs"
	. "github.com/vivowares/eywa/loggers"
	. "github.com/vivowares/eywa/utils"
	"strconv"
	"sync"
	"time"
)

var defaultWSCM *WebSocketConnectionManager

var noWscmErr = errors.New("connection manager is not initialized")
var closedWscmErr = errors.New("connection manager is closed")

func NewHttpConnection(id string, h MessageHandler, meta map[string]interface{}) (*HttpConnection, error) {
	conn := &HttpConnection{
		identifier: id,
		h:          h,
		metadata:   meta,
	}

	return conn, nil
}

func NewWebSocketConnection(id string, ws wsConn, h MessageHandler, meta map[string]interface{}) (*WebSocketConnection, error) {
	if defaultWSCM != nil {
		return defaultWSCM.newConnection(id, ws, h, meta)
	}

	return nil, noWscmErr
}

func WebSocketCount() int {
	count := 0

	if defaultWSCM != nil {
		count = defaultWSCM.count()
	}

	return count
}

func FindWeSocketConnection(id string) (*WebSocketConnection, bool) {
	if defaultWSCM != nil {
		return defaultWSCM.findConnection(id)
	}

	return nil, false
}

func InitializeWSCM() error {
	wscm, err := newWebSocketConnectionManager()
	defaultWSCM = wscm
	return err
}

func CloseWSCM() error {
	if defaultWSCM != nil {
		return defaultWSCM.close()
	}

	return noWscmErr
}

func newWebSocketConnectionManager() (*WebSocketConnectionManager, error) {
	wscm := &WebSocketConnectionManager{closed: &AtomBool{}}
	switch Config().WebSocketConnections.Registry {
	case "memory":
		wscm.Registry = &InMemoryRegistry{}
	default:
		wscm.Registry = &InMemoryRegistry{}
	}
	if err := wscm.Registry.Ping(); err != nil {
		return nil, err
	}

	wscm.shards = make([]*shard, Config().WebSocketConnections.NShards)
	for i := 0; i < Config().WebSocketConnections.NShards; i++ {
		wscm.shards[i] = &shard{
			wscm:    wscm,
			wsconns: make(map[string]*WebSocketConnection, Config().WebSocketConnections.InitShardSize),
		}
	}

	return wscm, nil
}

type WebSocketConnectionManager struct {
	closed   *AtomBool
	shards   []*shard
	Registry Registry
}

func (wscm *WebSocketConnectionManager) close() error {
	wscm.closed.Set(true)

	var wg sync.WaitGroup
	wg.Add(len(wscm.shards))
	for _, sh := range wscm.shards {
		go func(s *shard) {
			s.Close()
			wg.Done()
		}(sh)
	}
	wg.Wait()
	return wscm.Registry.Close()
}

func (wscm *WebSocketConnectionManager) newConnection(id string, ws wsConn, h MessageHandler, meta map[string]interface{}) (*WebSocketConnection, error) {
	if wscm.closed.Get() {
		ws.Close()
		return nil, closedWscmErr
	}

	hasher := murmur3.New32()
	hasher.Write([]byte(id))
	shard := wscm.shards[hasher.Sum32()%uint32(len(wscm.shards))]

	t := time.Now()
	conn := &WebSocketConnection{
		shard:        shard,
		ws:           ws,
		identifier:   id,
		createdAt:    t,
		lastPingedAt: t,
		h:            h,
		metadata:     meta,

		wch: make(chan *MessageReq, Config().WebSocketConnections.RequestQueueSize),
		msgChans: &syncRespChanMap{
			m: make(map[string]chan *MessageResp),
		},
		closewch: make(chan bool, 1),
		rch:      make(chan struct{}),
	}

	ws.SetPingHandler(func(payload string) error {
		conn.lastPingedAt = time.Now()
		conn.shard.updateRegistry(conn)
		Logger.Debug(fmt.Sprintf("connection: %s pinged", id))

		//extend the read deadline after each ping
		err := ws.SetReadDeadline(time.Now().Add(Config().WebSocketConnections.Timeouts.Read.Duration))
		if err != nil {
			return err
		}

		return ws.WriteControl(
			websocket.PongMessage,
			[]byte(strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10)),
			time.Now().Add(Config().WebSocketConnections.Timeouts.Write.Duration))
	})

	if err := shard.register(conn); err != nil {
		conn.Close()
		conn.Wait()
		return nil, err
	}

	conn.Start()

	return conn, nil
}

func (wscm *WebSocketConnectionManager) findConnection(id string) (*WebSocketConnection, bool) {
	hasher := murmur3.New32()
	hasher.Write([]byte(id))
	shard := wscm.shards[hasher.Sum32()%uint32(len(wscm.shards))]
	return shard.findConnection(id)
}

func (wscm *WebSocketConnectionManager) count() int {
	sum := 0
	for _, sh := range wscm.shards {
		sum += sh.Count()
	}
	return sum
}
