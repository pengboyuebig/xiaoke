package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Append(ScopeGlobal, "", "用户偏好简洁的中文回复")
	if err != nil || !strings.Contains(out, "已写入") {
		t.Fatalf("追加失败: %v %q", err, out)
	}
	got, err := s.Read(ScopeGlobal, "")
	if err != nil || !strings.Contains(got, "用户偏好简洁的中文回复") {
		t.Fatalf("读回失败: %v %q", err, got)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "- ") {
		t.Fatalf("记忆应为条目形态: %q", got)
	}
}

func TestAppendDeduplicates(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	if _, err := s.Append(ScopeGlobal, "", "同一条记忆"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Append(ScopeGlobal, "", "同一条记忆")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已存在") {
		t.Fatalf("重复追加应幂等跳过: %q", out)
	}
	got, _ := s.Read(ScopeGlobal, "")
	if strings.Count(got, "同一条记忆") != 1 {
		t.Fatalf("不应产生重复条目: %q", got)
	}
}

func TestProjectScopeIsolated(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	if _, err := s.Append(ScopeProject, "aaaa1111", "项目A的结构"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ScopeProject, "bbbb2222", "项目B的结构"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Read(ScopeProject, "aaaa1111")
	b, _ := s.Read(ScopeProject, "bbbb2222")
	if !strings.Contains(a, "项目A") || strings.Contains(a, "项目B") {
		t.Fatalf("项目 A 记忆混入了别项目内容: %q", a)
	}
	if !strings.Contains(b, "项目B") {
		t.Fatalf("项目 B 记忆丢失: %q", b)
	}
}

func TestInvalidProjectIDRejected(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	if _, err := s.Append(ScopeProject, "../evil", "x"); err == nil {
		t.Fatal("非法项目 ID 应被拒绝（防路径注入）")
	}
	if _, err := s.Append(ScopeProject, "", "x"); err == nil {
		t.Fatal("空项目 ID 应被拒绝")
	}
}

func TestAssemblyBudgetAndSections(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	if _, err := s.Append(ScopeGlobal, "", "全局知识一条"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ScopeProject, "cafe1234", "项目专属一条"); err != nil {
		t.Fatal(err)
	}
	asm := s.Assembly("cafe1234")
	if !strings.Contains(asm, "【全局记忆】") || !strings.Contains(asm, "【项目记忆】") {
		t.Fatalf("装配应含两层记忆: %q", asm)
	}
	// 未选项目时只注入全局
	asm2 := s.Assembly("")
	if !strings.Contains(asm2, "【全局记忆】") || strings.Contains(asm2, "【项目记忆】") {
		t.Fatalf("无项目时不应有项目段: %q", asm2)
	}
	// 空记忆返回空串
	if s2, _ := Open(filepath.Join(dir, "m2")); s2.Assembly("") != "" {
		t.Fatal("空记忆装配应为空串")
	}
}

func TestAssemblyTruncatesOversize(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	// 一条 8KB 的记忆，注入预算 4KB：必须截断并标注
	big := strings.Repeat("很", 8*1024)
	if _, err := s.Append(ScopeGlobal, "", big); err != nil {
		t.Fatal(err)
	}
	asm := s.Assembly("")
	if len(asm) > injectBudgetBytes+512 {
		t.Fatalf("装配超出预算: %d bytes", len(asm))
	}
	if !strings.Contains(asm, "已截断") {
		t.Fatal("截断应显式标注")
	}
}

func TestFileCap(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	// 塞满到 64KB 上限附近后，继续追加应报错而不是静默丢弃
	big := strings.Repeat("x", 40*1024) // 40KB（ASCII 每字符 1 字节）
	if _, err := s.Append(ScopeGlobal, "", big); err != nil {
		t.Fatalf("首次大条目应成功: %v", err)
	}
	if _, err := s.Append(ScopeGlobal, "", big+"-tail"); err == nil {
		t.Fatal("超出文件上限应报错")
	}
}

func TestNormalizeStripsZeroWidth(t *testing.T) {
	in := "\u200b带零宽字符\u200c的内容\ufeff\n\n\n\n多空行"
	out := normalizeMemoryText(in)
	if strings.ContainsAny(out, "\u200b\u200c\ufeff") {
		t.Fatalf("零宽字符应被剥离: %q", out)
	}
	if strings.Contains(out, "\n\n\n") {
		t.Fatalf("三个以上连续换行应被折叠: %q", out)
	}
}

// ImportGlobal：逐条去重导入、过滤太短/太长的行、返回真实新增数。
func TestImportGlobal(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	added, err := s.ImportGlobal([]string{
		"用户偏好简洁回复",                // 新增
		"用户偏好简洁回复",                // 重复：不计
		"项目 X 用 npm run build 构建", // 新增
		"短",                       // 太短：跳过
		strings.Repeat("长", 400),  // 太长：跳过
		"",                        // 空：跳过
	})
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("应新增 2 条，got %d", added)
	}
	got, _ := s.Read(ScopeGlobal, "")
	if strings.Count(got, "用户偏好简洁回复") != 1 || !strings.Contains(got, "npm run build") {
		t.Fatalf("导入结果不符: %q", got)
	}
}

func TestWriteGlobalOverwrites(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "memory"))
	if _, err := s.Append(ScopeGlobal, "", "旧内容"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteGlobal("- 手工整理后的记忆\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(ScopeGlobal, "")
	if strings.Contains(got, "旧内容") || !strings.Contains(got, "手工整理") {
		t.Fatalf("覆写失败: %q", got)
	}
	// 缓存一致性：覆写后再 Append 应基于新内容
	if _, err := s.Append(ScopeGlobal, "", "手工整理后的记忆"); err != nil {
		t.Fatal(err)
	}
	out, _ := s.Read(ScopeGlobal, "")
	if strings.Count(out, "手工整理后的记忆") != 1 {
		t.Fatalf("覆写后缓存未刷新导致重复: %q", out)
	}
}
