package natsrpc

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

// 端点元数据的键，见 docs/rpc/protocol.md 的服务发现。
const (
	MetadataPermissions = "tenon.permissions"
	MetadataIdempotent  = "tenon.idempotent"
	MetadataDescription = "tenon.description"
)

// endpointStats 是一个端点的统计，格式与服务协议的 $SRV.STATS 相同。
type endpointStats struct {
	mu        sync.Mutex
	requests  int
	errors    int
	lastError string
	total     time.Duration
}

func (s *endpointStats) record(d time.Duration, failed string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	s.total += d
	if failed != "" {
		s.errors++
		s.lastError = failed
	}
}

// subscribeControl 订阅服务协议的 $SRV.PING、$SRV.INFO、$SRV.STATS：不带名字、带服务名、带服务名和实例 ID 三种 subject，
// 每个实例都应答，不加入队列组。
func (r *runner) subscribeControl(nc *nats.Conn, endpoints []*endpoint) ([]*nats.Subscription, error) {
	identity := micro.ServiceIdentity{Name: r.service, ID: r.id, Version: r.opts.Version, Metadata: map[string]string{}}
	responses := map[micro.Verb]func() any{
		micro.PingVerb:  func() any { return micro.Ping{ServiceIdentity: identity, Type: micro.PingResponseType} },
		micro.InfoVerb:  func() any { return r.info(identity, endpoints) },
		micro.StatsVerb: func() any { return r.stats(identity, endpoints) },
	}
	var subs []*nats.Subscription
	for verb, response := range responses {
		for _, subject := range []struct{ name, id string }{{"", ""}, {r.service, ""}, {r.service, r.id}} {
			subj, err := micro.ControlSubject(verb, subject.name, subject.id)
			if err == nil {
				var sub *nats.Subscription
				sub, err = nc.Subscribe(subj, func(m *nats.Msg) {
					data, _ := json.Marshal(response())
					_ = m.Respond(data)
				})
				if err == nil {
					subs = append(subs, sub)
					continue
				}
			}
			for _, sub := range subs {
				_ = sub.Unsubscribe()
			}
			return nil, errors.Wrapf(err, "natsrpc: subscribe %s", subj)
		}
	}
	return subs, nil
}

func (r *runner) info(identity micro.ServiceIdentity, endpoints []*endpoint) micro.Info {
	info := micro.Info{ServiceIdentity: identity, Type: micro.InfoResponseType, Description: r.opts.Description, Endpoints: []micro.EndpointInfo{}}
	for _, ep := range endpoints {
		info.Endpoints = append(info.Endpoints, micro.EndpointInfo{
			Name:       ep.name,
			Subject:    ep.subject,
			QueueGroup: micro.DefaultQueueGroup,
			Metadata:   metadata(ep.spec),
		})
	}
	return info
}

func (r *runner) stats(identity micro.ServiceIdentity, endpoints []*endpoint) micro.Stats {
	stats := micro.Stats{ServiceIdentity: identity, Type: micro.StatsResponseType, Started: r.started, Endpoints: []*micro.EndpointStats{}}
	for _, ep := range endpoints {
		ep.stats.mu.Lock()
		es := &micro.EndpointStats{
			Name:           ep.name,
			Subject:        ep.subject,
			QueueGroup:     micro.DefaultQueueGroup,
			NumRequests:    ep.stats.requests,
			NumErrors:      ep.stats.errors,
			LastError:      ep.stats.lastError,
			ProcessingTime: ep.stats.total,
		}
		if ep.stats.requests > 0 {
			es.AverageProcessingTime = ep.stats.total / time.Duration(ep.stats.requests)
		}
		ep.stats.mu.Unlock()
		stats.Endpoints = append(stats.Endpoints, es)
	}
	return stats
}

// metadata 返回端点在服务发现中的元数据。
func metadata(e Endpoint) map[string]string {
	idempotent := "false"
	if e.Idempotent {
		idempotent = "true"
	}
	return map[string]string{
		MetadataPermissions: strings.Join(e.Permissions, ","),
		MetadataIdempotent:  idempotent,
		MetadataDescription: e.Description,
	}
}
