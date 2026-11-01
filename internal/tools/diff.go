// 行级统一 diff：edit_file / write_file 修改已有文件时生成变更预览。
// 只服务于前端展示（tool_result 事件的 diff 字段），不进模型上下文——
// 模型自己知道改了什么，重复喂一遍 diff 是纯浪费。
package tools

import (
	"fmt"
	"strings"
)

const (
	// maxDiffLines 输出上限：超出截中缝（前端是预览，不是审计工具）。
	maxDiffLines = 400
	// dpLimit LCS 动态规划的行数限额：先裁公共前后缀，中间区超过限额时
	// 退化为整块替换（O(n*m) 内存对超大文件不可接受）。
	dpLimit = 2500
)

// UnifiedDiff 对比旧/新文本，产出带 @@ 行号的统一 diff（上下文 ctxLines 行）。
// CRLF 归一化：比较与输出都按 LF。
func UnifiedDiff(oldText, newText string, ctxLines int) string {
	if oldText == newText {
		return ""
	}
	if ctxLines < 1 {
		ctxLines = 1
	}
	a := splitLines(oldText)
	b := splitLines(newText)

	// 裁公共前后缀：绝大多数编辑是局部改动，把 DP 范围缩到中间变更区
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var blocks []diffOp
	if len(am) > dpLimit || len(bm) > dpLimit {
		blocks = []diffOp{{op: opReplace, a1: 0, a2: len(am), b1: 0, b2: len(bm)}}
	} else {
		blocks = lcsBlocks(am, bm)
	}
	if len(blocks) == 0 {
		return ""
	}
	// 块坐标平移到全文件行号（后续上下文行都要从 a/b 取）
	for i := range blocks {
		blocks[i].a1 += pre
		blocks[i].a2 += pre
		blocks[i].b1 += pre
		blocks[i].b2 += pre
	}

	// 相邻块的间隔 ≤ 2×上下文 时上下文会重叠，合并进同一个 hunk
	type hunk struct {
		a1, a2, b1, b2 int
		blocks         []diffOp
	}
	var hunks []hunk
	for _, blk := range blocks {
		if len(hunks) > 0 && blk.a1-hunks[len(hunks)-1].a2 <= 2*ctxLines {
			h := &hunks[len(hunks)-1]
			h.blocks = append(h.blocks, blk)
			h.a2, h.b2 = blk.a2, blk.b2
			continue
		}
		hunks = append(hunks, hunk{a1: blk.a1, a2: blk.a2, b1: blk.b1, b2: blk.b2, blocks: []diffOp{blk}})
	}

	var out []string
	emit := func(s string) { out = append(out, s) }
	for _, hk := range hunks {
		before := hk.a1
		if before > ctxLines {
			before = ctxLines
		}
		after := len(a) - hk.a2 // 尾部上下文可以伸进公共后缀区（那是真实存在的上下文行）
		if after > ctxLines {
			after = ctxLines
		}
		emit(fmt.Sprintf("@@ -%s +%s @@",
			rangeHead(hk.a1-before+1, hk.a2-hk.a1+before+after),
			rangeHead(hk.b1-before+1, hk.b2-hk.b1+before+after)))
		for i := hk.a1 - before; i < hk.a1; i++ {
			emit(" " + a[i])
		}
		ai, bi := hk.a1, hk.b1
		for _, blk := range hk.blocks {
			for ai < blk.a1 { // 块间未变行按上下文输出
				emit(" " + a[ai])
				ai++
				bi++
			}
			if blk.op != opInsert {
				for i := blk.a1; i < blk.a2; i++ {
					emit("-" + a[i])
				}
			}
			if blk.op != opDelete {
				for i := blk.b1; i < blk.b2; i++ {
					emit("+" + b[i])
				}
			}
			ai, bi = blk.a2, blk.b2
		}
		for i := 0; i < after; i++ {
			emit(" " + a[ai+i])
		}
	}
	if len(out) > maxDiffLines {
		head := maxDiffLines * 2 / 3
		tail := append([]string(nil), out[len(out)-maxDiffLines/6:]...)
		out = append(out[:head:head], "…（diff 过长已截断）")
		out = append(out, tail...)
	}
	return strings.Join(out, "\n")
}

// rangeHead 统一 diff 区间头："start,count"，count==1 时按惯例省略 count。
func rangeHead(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// splitLines 按 \n 切行并去掉行尾 \r；末尾无换行也算一行。
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // 尾部换行产生的空串不是内容行
	}
	return lines
}

type diffOpKind int

const (
	opEqual diffOpKind = iota
	opDelete
	opInsert
	opReplace
)

type diffOp struct {
	op     diffOpKind
	a1, a2 int // 旧文本行区间 [a1,a2)
	b1, b2 int // 新文本行区间 [b1,b2)
}

// lcsBlocks 用动态规划求最长公共子序列，把非公共行压成连续的编辑块。
// 调用方保证两侧行数 ≤ dpLimit。
func lcsBlocks(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([]int32, (n+1)*(m+1))
	get := func(i, j int) int32 { return dp[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i*(m+1)+j] = get(i+1, j+1) + 1
			case get(i+1, j) >= get(i, j+1):
				dp[i*(m+1)+j] = get(i+1, j)
			default:
				dp[i*(m+1)+j] = get(i, j+1)
			}
		}
	}
	// 逐行回溯成 (del,ins) 步骤，再把连续的非公共步骤合并成块
	type step struct{ del, ins int }
	var steps []step
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case get(i+1, j) >= get(i, j+1):
			steps = append(steps, step{del: i, ins: -1})
			i++
		default:
			steps = append(steps, step{ins: j, del: -1})
			j++
		}
	}
	for ; i < n; i++ {
		steps = append(steps, step{del: i, ins: -1})
	}
	for ; j < m; j++ {
		steps = append(steps, step{ins: j, del: -1})
	}
	var ops []diffOp
	for k := 0; k < len(steps); {
		a1, a2, b1, b2 := -1, -1, -1, -1
		for k < len(steps) && (steps[k].del >= 0 || steps[k].ins >= 0) {
			if d := steps[k].del; d >= 0 {
				if a1 == -1 {
					a1 = d
				}
				a2 = d + 1
			}
			if n2 := steps[k].ins; n2 >= 0 {
				if b1 == -1 {
					b1 = n2
				}
				b2 = n2 + 1
			}
			k++
		}
		at := func(v int) int {
			if v < 0 {
				return 0
			}
			return v
		}
		op := opDelete
		switch {
		case a1 == -1:
			op = opInsert
		case b1 != -1:
			op = opReplace
		}
		ops = append(ops, diffOp{op: op, a1: at(a1), a2: at(a2), b1: at(b1), b2: at(b2)})
	}
	return ops
}
