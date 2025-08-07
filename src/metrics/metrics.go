package metrics

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
)

var (
	liveStatus = prometheus.NewDesc(
		prometheus.BuildFQName("bgo", "live", "status"),
		"live status",
		[]string{"live_id", "live_url", "live_host_name", "live_room_name", "live_listening"},
		nil,
	)
	liveDurationSeconds = prometheus.NewDesc(
		prometheus.BuildFQName("bgo", "live", "duration_seconds"),
		"live status",
		[]string{"live_id", "live_url", "live_host_name", "live_room_name", "start_time"},
		nil,
	)
	recorderTotalBytes = prometheus.NewDesc(
		prometheus.BuildFQName("bgo", "recorder", "total_bytes"),
		"recorder total bytes",
		[]string{"live_id", "live_url", "live_host_name", "live_room_name"},
		nil,
	)
)

type collector struct {
	inst *instance.Instance
}

func NewCollector(ctx context.Context) interfaces.Module {
	return &collector{
		inst: instance.GetInstance(ctx),
	}
}

func bool2float64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
	wg := sync.WaitGroup{}
	for id, l := range c.inst.Lives {
		wg.Add(1)
		go func(id types.LiveID, l live.Live) {
			defer wg.Done()
			obj, err := c.inst.Cache.Get(l)
			if err != nil {
				return
			}
			info := obj.(*live.Info)
			listening := c.inst.ListenerManager.(listeners.Manager).HasListener(context.Background(), id)
			ch <- prometheus.MustNewConstMetric(
				liveStatus, prometheus.GaugeValue, bool2float64(info.Status),
				string(id), l.GetRawUrl(), info.HostName, info.RoomName, fmt.Sprintf("%v", listening),
			)

			if info.Status && listening {
				ch <- prometheus.MustNewConstMetric(
					liveDurationSeconds, prometheus.CounterValue, time.Since(l.GetLastStartTime()).Seconds(),
					string(id), l.GetRawUrl(), info.HostName, info.RoomName, strconv.FormatInt(info.Live.GetLastStartTime().Unix(), 10),
				)

				if r, err := c.inst.RecorderManager.(recorders.Manager).GetRecorder(context.Background(), id); err == nil {
					if status, err := r.GetStatus(); err == nil {
						if value, err := strconv.ParseFloat(status["total_size"], 64); err == nil {
							ch <- prometheus.MustNewConstMetric(recorderTotalBytes, prometheus.CounterValue, value,
								string(id), l.GetRawUrl(), info.HostName, info.RoomName)
						}
					}
				}
			}
		}(id, l)
	}
	wg.Wait()
}

func (collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- liveStatus
	ch <- liveDurationSeconds
	ch <- recorderTotalBytes
}

func (c *collector) Start(_ context.Context) error {
	return prometheus.Register(c)
}

func (c *collector) Close(_ context.Context) {}
