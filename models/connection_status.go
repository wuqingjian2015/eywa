package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vivowares/eywa/Godeps/_workspace/src/gopkg.in/olivere/elastic.v3"
	. "github.com/vivowares/eywa/connections"
	. "github.com/vivowares/eywa/utils"
	"time"
)

var HistoryLength = 100

type ConnectionStatus struct {
	ChannelName    string
	Status         string
	ConnectedAt    time.Time
	DisconnectedAt time.Time
	ConnectionType string
	Duration       time.Duration
	LastPingedAt   time.Time
	Identifier     string
	Metadata       map[string]string
	Histories      []*ConnectionHistory
}

func (h *ConnectionStatus) MarshalJSON() ([]byte, error) {
	j := make(map[string]interface{})

	if len(h.ChannelName) > 0 {
		j["channel_name"] = h.ChannelName
	}

	if len(h.Status) > 0 {
		j["status"] = h.Status
	}

	if !h.ConnectedAt.IsZero() {
		j["connected_at"] = NanoToMilli(h.ConnectedAt.UnixNano())
	}

	if !h.DisconnectedAt.IsZero() {
		j["disconnected_at"] = NanoToMilli(h.DisconnectedAt.UnixNano())
	}

	if len(h.ConnectionType) > 0 {
		j["connection_type"] = h.ConnectionType
	}

	if int64(h.Duration) > 0 {
		j["duration"] = NanoToMilli(h.Duration.Nanoseconds())
	}

	if !h.LastPingedAt.IsZero() {
		j["last_pinged_at"] = NanoToMilli(h.LastPingedAt.UnixNano())
	}

	if len(h.Identifier) > 0 {
		j["device_id"] = h.Identifier
	}

	if h.Metadata != nil && len(h.Metadata) > 0 {
		for k, v := range h.Metadata {
			j[k] = v
		}
	}

	if h.Histories != nil && len(h.Histories) > 0 {
		j["connection_history"] = h.Histories
	}

	return json.Marshal(j)

}

type ConnectionHistory struct {
	Ip             string
	RequestId      string
	Activity       string
	Timestamp      time.Time
	ConnectionType string
	Duration       time.Duration
}

func (h *ConnectionHistory) MarshalJSON() ([]byte, error) {
	j := make(map[string]interface{})
	if len(h.Ip) > 0 {
		j["ip"] = h.Ip
	}

	if len(h.RequestId) > 0 {
		j["request_id"] = h.RequestId
	}

	if len(h.Activity) > 0 {
		j["activity"] = h.Activity
	}

	if len(h.ConnectionType) > 0 {
		j["connection_type"] = h.ConnectionType
	}

	if !h.Timestamp.IsZero() {
		j["timestamp"] = NanoToMilli(h.Timestamp.UnixNano())
	}

	if int64(h.Duration) > 0 {
		j["duration"] = NanoToMilli(h.Duration.Nanoseconds())
	}

	return json.Marshal(j)
}

func (h *ConnectionHistory) UnmarshalJSON(data []byte) error {
	j := make(map[string]interface{})
	err := json.Unmarshal(data, &j)
	if err != nil {
		return err
	}

	if ip, found := j["ip"]; found {
		h.Ip = ip.(string)
	}

	if reqId, found := j["request_id"]; found {
		h.RequestId = reqId.(string)
	}

	if connType, found := j["connection_type"]; found {
		h.ConnectionType = connType.(string)
	}

	if act, found := j["activity"]; found {
		h.Activity = act.(string)
	}

	if ts, found := j["timestamp"]; found {
		milli := int64(ts.(float64))
		h.Timestamp = time.Unix(MilliSecToSec(milli), MilliSecToNano(milli))
	}

	if dur, found := j["duration"]; found {
		milli := int64(dur.(float64))
		h.Duration = time.Duration(milli) * time.Millisecond
	}

	return nil
}

func FindConnectionStatus(ch *Channel, devId string, withHistory bool) (*ConnectionStatus, error) {
	name, err := ch.HashId()
	if err != nil {
		return nil, err
	}

	cm, found := FindConnectionManager(name)
	if !found {
		return nil, errors.New(fmt.Sprintf("connection manager is not initialized for channel: %s", name))
	}

	s := &ConnectionStatus{
		ChannelName: ch.Name,
		Status:      "offline",
		Identifier:  devId,
		Histories:   make([]*ConnectionHistory, 0),
	}

	conn, found := cm.FindConnection(devId)
	if found {
		s.Status = "online"
		s.ConnectedAt = conn.CreatedAt()
		s.LastPingedAt = conn.LastPingedAt()
		s.ConnectionType = conn.ConnectionType()
		s.Metadata = conn.Metadata()
		s.Duration = time.Now().Sub(conn.CreatedAt())
	}

	if withHistory {
		boolQ := elastic.NewBoolQuery()
		boolQ.Must(elastic.NewTermQuery("device_id", devId))
		boolQ.Should(elastic.NewTermQuery("message_type", SupportedMessageTypes[TypeConnectMessage]))
		boolQ.Should(elastic.NewTermQuery("message_type", SupportedMessageTypes[TypeDisconnectMessage]))

		resp, err := IndexClient.Search().Index(GlobalIndexName(ch)).Type(IndexTypeActivities).Query(boolQ).Sort("timestamp", false).Size(HistoryLength).Do()
		if err == nil {
			for _, hit := range resp.Hits.Hits {
				h := &ConnectionHistory{}
				err := json.Unmarshal(*hit.Source, h)
				if err != nil {
					return nil, err
				} else {
					s.Histories = append(s.Histories, h)
				}
			}
		} else {
			return nil, err
		}
	}

	return s, nil
}
