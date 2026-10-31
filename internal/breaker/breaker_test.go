package breaker

import (
	"sync"
	"testing"
	"time"
)

// shortenOpen 缩短冷却时长（测试提速），返回恢复函数。
func shortenOpen(t *testing.T, d time.Duration) func() {
	t.Helper()
	old := openDuration
	openDuration = d
	return func() { openDuration = old }
}

func forceFailWindow(b *Breaker, n int) {
	for i := 0; i < n; i++ {
		b.Record(false)
	}
}

func TestBreakerOpensAndProbes(t *testing.T) {
	defer shortenOpen(t, 20*time.Millisecond)()
	r := NewRegistry()
	b := r.For("https://api.test.com|m1")

	// 样本不足：不跳闸
	forceFailWindow(b, minSamples-1)
	if d := b.Allow(); d != Allow {
		t.Fatalf("样本不足不应跳闸: %v", d)
	}

	// 第 5 次失败：错误率 100% ≥ 50%，跳闸
	b.Record(false)
	if d := b.Allow(); d != Tripped {
		t.Fatalf("错误率超阈值应跳闸: %v", d)
	}

	// 熔断期内继续失败不会重置冷却
	b.Record(false)
	if d := b.Allow(); d != Tripped {
		t.Fatalf("熔断期内应快速失败: %v", d)
	}

	// 冷却到期：放一个探测请求
	time.Sleep(40 * time.Millisecond)
	if d := b.Allow(); d != Probe {
		t.Fatalf("冷却到期应放行探测: %v", d)
	}
	// 探测期间后续请求仍被拒
	if d := b.Allow(); d != Tripped {
		t.Fatalf("探测期间应继续拒绝: %v", d)
	}
	// 探测成功 → 恢复
	b.Record(true)
	if d := b.Allow(); d != Allow {
		t.Fatalf("探测成功后应恢复放行: %v", d)
	}
}

func TestBreakerProbeFailureReopens(t *testing.T) {
	defer shortenOpen(t, 20*time.Millisecond)()
	b := &Breaker{}
	forceFailWindow(b, minSamples)
	if d := b.Allow(); d != Tripped {
		t.Fatalf("应跳闸: %v", d)
	}
	time.Sleep(40 * time.Millisecond)
	if d := b.Allow(); d != Probe {
		t.Fatalf("应放行探测: %v", d)
	}
	// 探测失败：保持熔断（重新计时）
	b.Record(false)
	if d := b.Allow(); d != Tripped {
		t.Fatalf("探测失败应继续熔断: %v", d)
	}
}

func TestBreakerRecoversOnSuccesses(t *testing.T) {
	b := &Breaker{}
	// 2 失败 + 3 成功 = 40% 错误率（< 50%），不跳闸
	for i := 0; i < 2; i++ {
		b.Record(false)
	}
	for i := 0; i < 3; i++ {
		b.Record(true)
	}
	if d := b.Allow(); d != Allow {
		t.Fatalf("错误率未达阈值不应跳闸: %v", d)
	}
	// 对照：3 失败 + 2 成功 = 60%，应跳闸
	b2 := &Breaker{}
	for i := 0; i < 3; i++ {
		b2.Record(false)
	}
	for i := 0; i < 2; i++ {
		b2.Record(true)
	}
	if d := b2.Allow(); d != Tripped {
		t.Fatalf("错误率 60%% 应跳闸: %v", d)
	}
}

func TestBreakerWindowExpiry(t *testing.T) {
	b := &Breaker{}
	forceFailWindow(b, minSamples)
	if d := b.Allow(); d != Tripped {
		t.Fatalf("应跳闸: %v", d)
	}
	// 把所有槽位的时间戳改到窗口外：统计样本归零，失败率不可信 → 熔断应能被新成功流量冲开
	// （模拟：很久没流量后，旧的失败不应永久锁死熔断器）
	now := time.Now().UnixNano()
	old := now - int64(2*windowDuration)
	for i := range b.stamps {
		b.stamps[i] = old
	}
	b.openedAt = time.Now().Add(-2 * openDuration)
	if d := b.Allow(); d != Probe {
		t.Fatalf("过期窗口+冷却到期应放行探测: %v", d)
	}
	b.Record(true)
	if d := b.Allow(); d != Allow {
		t.Fatalf("探测成功后应恢复: %v", d)
	}
}

func TestRegistryPerKey(t *testing.T) {
	r := NewRegistry()
	a := r.For("https://a.com|m1")
	c := r.For("https://c.com|m2")
	forceFailWindow(a, minSamples)
	if a.Allow() != Tripped {
		t.Fatal("a 应熔断")
	}
	if c.Allow() != Allow {
		t.Fatal("c 不应被 a 连坐")
	}
	// 同 key 复用同一实例
	if r.For("https://a.com|m1") != a {
		t.Fatal("同 key 应复用同一熔断器")
	}
}

// 并发安全：-race 下并发 Record/Allow 不出 data race。
func TestBreakerConcurrent(t *testing.T) {
	b := &Breaker{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b.Allow()
				b.Record(j%3 != 0)
			}
		}(i)
	}
	wg.Wait()
}
