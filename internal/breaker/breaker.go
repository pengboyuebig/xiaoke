// Package breaker 实现熔断器：保护对 LLM 上游的连续失败。
//
// 对标主流服务的 circuit breaker 实践（60s 滑窗 + 错误率阈值 + 半开探测）：
// 上游持续 4xx/5xx 时快速失败（不再把请求扔向一个正在故障的端点），
// 冷却后放一个探测请求验证恢复。
//
// 定位：熔断的是「同一配置的上游」，Key 是 baseURL+model——多模型场景下
// 一个模型故障不应拖垮另一个模型的调用。
package breaker

import (
	"sync"
	"time"
)

const (
	// windowSize 是滑动窗口的记录槽数。
	windowSize = 20
	// windowDuration 是统计窗口：只数最近这段时间的请求。
	windowDuration = 60 * time.Second
	// minSamples 是窗口内至少这么多次请求才统计错误率（样本太少不跳闸）。
	minSamples = 5
	// errorRateThreshold 是跳闸的错误率（50%）。
	errorRateThreshold = 0.5
)

// openDuration 是熔断打开的冷却时长；到期转半开放一个探测请求。
// var 仅为让测试缩短等待（测试里 SaveOpenDuration 恢复）；生产代码不得修改。
var openDuration = 10 * time.Second

// Decision 是 Allow 的判定结果：
//
//	Allow: 正常放行
//	Probe: 半开——放行这一个探测请求（后续请求仍是 Tripped，直到探测结果）
//	Tripped: 熔断中，调用方应快速失败
type Decision int

const (
	Allow Decision = iota
	Probe
	Tripped
)

// Breaker 是单个上游的熔断器（由 Registry 按 key 持有）。
type Breaker struct {
	mu sync.Mutex

	// 滑动窗口：环形记录最近 windowSize 次请求的结果与时间
	stamps   [windowSize]int64 // unix nanos；0 = 空槽
	failures [windowSize]bool
	head     int
	filled   int

	openedAt time.Time // 零值 = 未熔断
}

// Allow 判断当前是否放行。
func (b *Breaker) Allow() Decision {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openedAt.IsZero() {
		return Allow
	}
	if time.Since(b.openedAt) < openDuration {
		return Tripped
	}
	// 冷却到期 → 半开：立即重新计时，只放行这一个探测请求。
	// 探测成功时 Record 复位；失败时保持打开（重新计时）。
	b.openedAt = time.Now()
	return Probe
}

// Record 记录一次请求结果（success=false 表示失败：非 200、网络错误、流中断）。
// 成功且处于熔断态 = 探测成功，复位；随后按窗口统计决定是否（重新）跳闸。
func (b *Breaker) Record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stamps[b.head] = time.Now().UnixNano()
	b.failures[b.head] = !success
	b.head = (b.head + 1) % windowSize
	if b.filled < windowSize {
		b.filled++
	}
	if !b.openedAt.IsZero() && success {
		b.openedAt = time.Time{} // 探测成功，恢复放行
		return
	}
	// 跳闸检查：样本足够且错误率超阈值（半开探测失败也走这里重新计时）
	if b.openedAt.IsZero() {
		samples, failures := b.statsLocked()
		if samples >= minSamples && float64(failures)/float64(samples) >= errorRateThreshold {
			b.openedAt = time.Now()
		}
	}
}

// statsLocked 统计窗口内的有效样本数与失败数（调用方需持锁）。
func (b *Breaker) statsLocked() (samples, failures int) {
	cutoff := time.Now().Add(-windowDuration).UnixNano()
	for i := 0; i < b.filled; i++ {
		if b.stamps[i] == 0 || b.stamps[i] < cutoff {
			continue
		}
		samples++
		if b.failures[i] {
			failures++
		}
	}
	return
}

// Registry 是按 key 复用的熔断器集合（并发安全）。
type Registry struct {
	mu       sync.Mutex
	breakers map[string]*Breaker
}

func NewRegistry() *Registry {
	return &Registry{breakers: map[string]*Breaker{}}
}

// For 返回 key（如 "https://api.deepseek.com|deepseek-chat"）对应的熔断器。
func (r *Registry) For(key string) *Breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.breakers[key]
	if !ok {
		b = &Breaker{}
		r.breakers[key] = b
	}
	return b
}
