// 会话内交互通道：ask_user 工具与权限审批共用的「等用户回答」注册表。
//
// 模型侧工具执行 goroutine 阻塞在 question.answerCh 上；HTTP 侧经
// Resolve 把答案投进 channel。qid 是一次性凭证（取用即删），重复提交
// 报 ErrQuestionGone。5 分钟无人回答超时放行（返回 timeout 语义给模型）。
package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const questionTimeout = 5 * time.Minute

var (
	ErrQuestionGone = errors.New("问题已失效（已回答或超时）")
	ErrTimeout      = errors.New("用户未在限时内回答")
)

// PendingQuestion 是一个等待用户回答的提问。
type PendingQuestion struct {
	ID      string
	Kind    string // "question"（ask_user）| "permission"（ask 模式审批）
	Session string
	Payload map[string]any // SSE 事件载荷（question/options/tool/summary…）
	Ch      chan string
}

// questionRegistry 是 qid → 等待中的提问。
type questionRegistry struct {
	mu    sync.Mutex
	items map[string]*PendingQuestion
}

func newQuestionRegistry() *questionRegistry {
	return &questionRegistry{items: map[string]*PendingQuestion{}}
}

// Register 登记一个提问并分配 qid（调用方从返回值取 ID）。
func (r *questionRegistry) Register(q *PendingQuestion) *PendingQuestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	q.ID = "q_" + fmt.Sprintf("%d_%d", time.Now().UnixNano(), len(r.items))
	// 极小概率同纳秒撞号：尾巴再加一段随机
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err == nil {
		q.ID += "_" + hex.EncodeToString(rnd[:])
	}
	r.items[q.ID] = q
	return q
}

// Resolve 把用户答案投给等待方；qid 不存在/已取用返回 false。
func (r *questionRegistry) Resolve(qid, answer string) bool {
	r.mu.Lock()
	q, ok := r.items[qid]
	delete(r.items, qid)
	r.mu.Unlock()
	if !ok {
		return false
	}
	q.Ch <- answer
	close(q.Ch)
	return true
}

// Take 取走提问（超时清理用），取走后 Resolve 不再生效。
func (r *questionRegistry) Take(qid string) *PendingQuestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.items[qid]
	delete(r.items, qid)
	return q
}

// AskWait 阻塞等待回答；ctx 取消或超时返回错误（不占悬挂 goroutine）。
// 超时返回 ErrTimeout，调用方据此给模型「用户未响应」语义。
func AskWait(ctx context.Context, q *PendingQuestion) (string, error) {
	timer := time.NewTimer(questionTimeout)
	defer timer.Stop()
	select {
	case answer, ok := <-q.Ch:
		if !ok {
			return "", ErrQuestionGone
		}
		return answer, nil
	case <-timer.C:
		questions.Take(q.ID)
		return "", ErrTimeout
	case <-ctx.Done():
		questions.Take(q.ID)
		return "", ctx.Err()
	}
}

// questions 是进程级注册表（单服务实例足够；qid 为随机串不可猜）。
var questions = newQuestionRegistry()
