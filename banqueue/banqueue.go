// Package banqueue 保存「订阅防泄漏」的拉黑事件，供定时任务取走并推送通知。
//
// 单独放一个无依赖的包，避免 sub 与 web/job 之间形成循环引用
// （sub 已经引用了 web，若 web/job 再引用 sub 就成环）。
package banqueue

import (
	"sync"
	"time"
)

// Event 是一次拉黑记录。
type Event struct {
	IP     string
	Reason string
	At     int64
}

var (
	mu     sync.Mutex
	events []Event
)

// Record 记一次拉黑。
func Record(ip, reason string) {
	mu.Lock()
	defer mu.Unlock()
	// 防止内存无限增长：超过 200 条就只留最近 100 条
	if len(events) >= 200 {
		events = events[len(events)-100:]
	}
	events = append(events, Event{IP: ip, Reason: reason, At: time.Now().Unix()})
}

// Drain 取走并清空累积的事件。
func Drain() []Event {
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		return nil
	}
	out := make([]Event, len(events))
	copy(out, events)
	events = nil
	return out
}
