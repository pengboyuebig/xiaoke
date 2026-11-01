// 5 段标准 cron 求值器：分 时 日 月 周（本地时区）。
// 支持：`*`、`*/n`、`a-b` 区间、`a,b,c` 列表及其组合（`1-15/2`）。
// 不支持：秒级、@yearly 等宏、年份字段、L/W 字符——工具描述已写明。
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule 是一条解析后的 cron 计划。
type Schedule struct {
	minute, hour, dom, month, dow map[int]bool
	domStar, dowStar              bool // 字段为纯 * 时才遵循「日周或」语义
}

type fieldSpec struct {
	min, max int
	names    map[string]int // 月/周的英文缩写（可选）
}

var (
	minSpec  = fieldSpec{min: 0, max: 59}
	hourSpec = fieldSpec{min: 0, max: 23}
	domSpec  = fieldSpec{min: 1, max: 31}
	monSpec  = fieldSpec{min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	dowSpec = fieldSpec{min: 0, max: 6, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

// Parse 解析 5 段 cron 表达式。
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("需要 5 段（分 时 日 月 周），got %d 段", len(fields))
	}
	minutes, err := parseField(fields[0], minSpec)
	if err != nil {
		return nil, fmt.Errorf("分钟段: %w", err)
	}
	hours, err := parseField(fields[1], hourSpec)
	if err != nil {
		return nil, fmt.Errorf("小时段: %w", err)
	}
	doms, domStar, err := parseDomDow(fields[2], domSpec)
	if err != nil {
		return nil, fmt.Errorf("日段: %w", err)
	}
	months, err := parseField(fields[3], monSpec)
	if err != nil {
		return nil, fmt.Errorf("月段: %w", err)
	}
	dows, dowStar, err := parseDomDow(fields[4], dowSpec)
	if err != nil {
		return nil, fmt.Errorf("周段: %w", err)
	}
	s := &Schedule{minute: minutes, hour: hours, dom: doms, month: months, dow: dows, domStar: domStar, dowStar: dowStar}
	// 空集校验（如 2 月 31 日）：向前探 4 年都找不到匹配就判定不可满足
	if s.Next(time.Now()).IsZero() {
		return nil, fmt.Errorf("该组合在日历上不存在（如 2 月 31 日）")
	}
	return s, nil
}

// parseField 解析单字段（*、*/n、a、a-b、a-b/n、逗号列表）。
func parseField(field string, spec fieldSpec) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			return nil, fmt.Errorf("空片段")
		}
		step := 1
		rangePart := part
		if i := strings.Index(part, "/"); i >= 0 {
			rangePart = part[:i]
			var err error
			step, err = strconv.Atoi(part[i+1:])
			if err != nil || step < 1 {
				return nil, fmt.Errorf("步长 %q 不合法", part[i+1:])
			}
		}
		lo, hi := spec.min, spec.max
		switch {
		case rangePart == "*":
			// 全范围
		case strings.Contains(rangePart, "-"):
			bits := strings.SplitN(rangePart, "-", 2)
			var err error
			lo, err = resolveValue(bits[0], spec)
			if err != nil {
				return nil, err
			}
			hi, err = resolveValue(bits[1], spec)
			if err != nil {
				return nil, err
			}
			if lo > hi {
				return nil, fmt.Errorf("区间 %q 起点大于终点", rangePart)
			}
		default:
			v, err := resolveValue(rangePart, spec)
			if err != nil {
				return nil, err
			}
			lo, hi = v, v
			if step > 1 {
				hi = spec.max // */n 或 a/n：从起点铺到字段上限
			}
		}
		if lo < spec.min || hi > spec.max {
			return nil, fmt.Errorf("值超出范围 [%d,%d]", spec.min, spec.max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}

// parseDomDow 额外标记纯 *（标准 cron 的「日或周」语义只在两者均为 * 时取「日」，
// 其余取「日与周同时满足」；本实现采用常见简化：dom 与 dow 都非 * 时要求同时命中）。
func parseDomDow(field string, spec fieldSpec) (map[int]bool, bool, error) {
	trimmed := strings.TrimSpace(field)
	if trimmed == "*" {
		out := map[int]bool{}
		for v := spec.min; v <= spec.max; v++ {
			out[v] = true
		}
		return out, true, nil
	}
	out, err := parseField(field, spec)
	return out, false, err
}

func resolveValue(tok string, spec fieldSpec) (int, error) {
	tok = strings.TrimSpace(strings.ToLower(tok))
	if v, ok := spec.names[tok]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("值 %q 不合法", tok)
	}
	return v, nil
}

// Next 返回 t 之后（不含 t）下一个匹配时刻（秒级对齐到 :00）。
// 线性前扫分钟粒度：最多扫 5 年 + 366 天防死循环；找不到返回零值。
func (s *Schedule) Next(after time.Time) time.Time {
	limit := after.Add(5*365*24*time.Hour + 366*24*time.Hour)
	t := after.Truncate(time.Minute).Add(time.Minute)
	for t.Before(limit) {
		if s.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

func (s *Schedule) matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domOK := s.dom[t.Day()]
	dowOK := s.dow[int(t.Weekday())]
	if s.domStar && s.dowStar {
		return true
	}
	if s.domStar {
		return dowOK
	}
	if s.dowStar {
		return domOK
	}
	return domOK && dowOK
}
