// 调用图：记录一次对话请求里 agent 之间的调用关系，负责三件事——
// 防环（A→B→A）、预算逐层扣减、乒乓上限（A↔B 往返次数）。
//
// 为什么必须有：agent 互调一旦放开，模型很容易陷入「你推给我、我推给你」。
// 真实事故里出现过 3 分钟自我调用 4 万次、以及两个 agent 互相矛盾调用
// 1700+ 次烧掉上万元的案例。所以这里的设计是「宁可拦错，不可放过」。
package chat

import (
	"fmt"
	"strings"
	"sync"
)

// 停止原因。对外展示时给用户一个明确的解释，而不是笼统的「调用失败」。
const (
	StopDepthCapped   = "depth_capped"   // 超过深度上限
	StopLoopCapped    = "loop_capped"    // 检测到调用环
	StopPingPong      = "pingpong_capped" // A↔B 往返超限
	StopTokenCapped   = "token_capped"   // 预算耗尽
	StopNotInCatalog  = "not_in_catalog" // 目标不在可调用列表
	StopNotAllowed    = "not_allowed"    // 白名单拒绝
)

// maxPingPongTurns 是两个 agent 之间往返调用的上限。
// 对齐 OpenClaw 的 agentToAgent.maxPingPongTurns 默认值 5。
const maxPingPongTurns = 5

// CallGraph 是一次对话请求内的调用图（请求结束即丢弃，不落库）。
type CallGraph struct {
	mu sync.Mutex
	// parent 记录每个节点（agent ID）的父节点。顶层是 ""。
	parent map[string]string
	// edgeCount 记录某条有向边被走过几次，用于乒乓检测。
	edgeCount map[string]int
	// toolCalls 记录每个 agent 发起的工具调用次数（供回放展示规模）。
	toolCalls map[string]int
	// stops 记录本次请求里被拦截的调用（用于 SSE 汇报）。
	stops []StopEvent
}

// StopEvent 是一次被拦截的调用。
type StopEvent struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

func newCallGraph(rootAgentID string) *CallGraph {
	g := &CallGraph{
		parent:    map[string]string{},
		edgeCount: map[string]int{},
		toolCalls: map[string]int{},
	}
	if rootAgentID != "" {
		g.parent[rootAgentID] = ""
	}
	return g
}

func edgeKey(from, to string) string { return from + "\x00" + to }

// Enter 尝试把 child 挂到 parent 下。返回 nil 表示允许调用；
// 返回 error 表示应当拒绝（环 / 乒乓超限）。
func (g *CallGraph) Enter(parent, child string) error {
	if parent == "" || child == "" {
		return nil
	}
	if parent == child {
		g.recordStop(parent, child, StopLoopCapped)
		return fmt.Errorf("智能体 %s 不能调用自己", child)
	}
	if g.wouldCycle(parent, child) {
		g.recordStop(parent, child, StopLoopCapped)
		return fmt.Errorf("检测到调用环（%s）：拒绝这次调用，避免两个智能体互相推诿。请直接完成你手上的部分。", g.chainOf(child))
	}
	if g.edgeCount[edgeKey(parent, child)]+1 > maxPingPongTurns {
		g.recordStop(parent, child, StopPingPong)
		return fmt.Errorf("%s 与 %s 之间已往返 %d 次，超过上限 %d：停止互相转派，各自把手上的事做完。",
			parent, child, maxPingPongTurns, maxPingPongTurns)
	}
	g.parent[child] = parent
	g.edgeCount[edgeKey(parent, child)]++
	return nil
}

// wouldCycle 判断 parent 调 child 是否会成环：从 parent 沿父链往上找，
// 如果遇到 child，说明 child 已经在 parent 的上游，再调就成环了。
func (g *CallGraph) wouldCycle(parent, child string) bool {
	cur := parent
	seen := map[string]bool{}
	for cur != "" {
		if cur == child {
			return true
		}
		if seen[cur] {
			return true // 数据异常时的兜底
		}
		seen[cur] = true
		cur = g.parent[cur]
	}
	return false
}

// chainOf 返回从顶层到 agent 的调用链（用于错误信息里展示）。
func (g *CallGraph) chainOf(agent string) string {
	parts := []string{}
	cur := agent
	seen := map[string]bool{}
	for cur != "" && !seen[cur] {
		parts = append([]string{cur}, parts...)
		seen[cur] = true
		cur = g.parent[cur]
	}
	return strings.Join(parts, " → ")
}

func (g *CallGraph) recordStop(from, to, reason string) {
	g.stops = append(g.stops, StopEvent{From: from, To: to, Reason: reason})
	if len(g.stops) > 32 {
		g.stops = g.stops[len(g.stops)-32:]
	}
}

// Stops 返回本次请求被拦截的调用列表。
func (g *CallGraph) Stops() []StopEvent {
	out := make([]StopEvent, len(g.stops))
	copy(out, g.stops)
	return out
}

// countUnder 返回某个 agent 在这条调用链里发起的工具调用次数。
func (g *CallGraph) countUnder(agentID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.toolCalls[agentID]
}

// addToolCalls 累加某个 agent 的工具调用次数（由 runAgent 结束时调用）。
func (g *CallGraph) addToolCalls(agentID string, n int) {
	if agentID == "" || n <= 0 {
		return
	}
	g.mu.Lock()
	g.toolCalls[agentID] += n
	g.mu.Unlock()
}

// BudgetTracker 是逐层递减的调用预算。
// 关键设计：预算必须从父扣减，不能每个 agent 各自独立——否则子 agent
// 可以绕过父的预算限制无限派活（"预算绕过"）。
type BudgetTracker struct {
	left int
}

func newBudgetTracker(total int) *BudgetTracker {
	if total < 0 {
		total = 0
	}
	return &BudgetTracker{left: total}
}

// Left 返回剩余可调用次数。
func (b *BudgetTracker) Left() int { return b.left }

// TryConsume 尝试消耗一次调用额度。成功返回 true。
func (b *BudgetTracker) TryConsume() bool {
	if b.left <= 0 {
		return false
	}
	b.left--
	return true
}
