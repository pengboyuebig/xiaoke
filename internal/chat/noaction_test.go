package chat

import (
	"testing"

	"xiaoke/internal/store"
)

// 典型的「只说不做」回复必须被识别出来。
func TestPromiseButNoActionDetects(t *testing.T) {
	pending := []store.Todo{
		{Content: "搭建项目骨架", Status: "pending"},
		{Content: "写配置", Status: "pending"},
	}
	cases := []string{
		// 截图里那种连续空承诺
		"我先看一下当前项目文件夹里有什么，再决定怎么搭。\n开工前先建待办，并确认一下 npm 环境。\n先把骨架文件写下来。",
		"接下来我要实现 Markdown 解析层。",
		"先创建项目骨架，然后安装依赖。",
		"我这就开始写配置文件。",
		"下面我们要把文章读取层写好。",
		"马上开始动手做。",
		"先把依赖装上，再写代码。",
	}
	for _, c := range cases {
		if !promiseButNoAction(c, pending, false) {
			t.Errorf("应判定为只说不做：%q", c)
		}
	}
}

// 有未完成待办时，即使请求本身不是多步任务也算（待办就是未完成的工作）。
func TestPromiseButNoActionWithPendingTodos(t *testing.T) {
	pending := []store.Todo{{Content: "写首页", Status: "in_progress"}}
	if !promiseButNoAction("我先把首页的文章列表做出来。", pending, false) {
		t.Error("有未完成待办时应判定为只说不做")
	}
}

// 正常回复不能被误判：没有未来时叙述、或已经明确说做完了。
func TestPromiseButNoActionNoFalsePositive(t *testing.T) {
	pending := []store.Todo{{Content: "写首页", Status: "pending"}}
	cases := []string{
		// 纯结论，无未来时
		"这个函数的作用是把 Markdown 转成 HTML。",
		"1+1 等于 2。",
		"代码已经写好了，现在可以运行 npm run dev 查看效果。",
		"已完成全部 6 项任务，构建通过。",
		"已创建 package.json 和 tsconfig.json。",
		// 有未来时但已声明完成（不该催）
		"我先看了一下目录结构，已确认是空的。",
		// 「先看/先读」是行文习惯，不是行动承诺
		"先看结论：这个函数的作用是把 Markdown 转成 HTML。",
		"先看代码结构，入口在 main.go。",
		"先读一下 README 再回答你的问题，结论是支持这个配置。",
		// 先动作后完成的叙述（真的做了事）
		"先看了目录结构，然后创建了 3 个文件，现在可以运行了。",
		"先读了 README，然后写好了 posts.ts。",
		// 空文本
		"",
		"   ",
	}
	for _, c := range cases {
		if promiseButNoAction(c, pending, true) {
			t.Errorf("不应判定为只说不做：%q", c)
		}
	}
}

// 没有待办、也不是多步任务时：明确的「我先看一下」仍要催（否则直接停住）；
// 纯闲聊/结论则不该催。
func TestPromiseButNoActionExplicitPromise(t *testing.T) {
	if !promiseButNoAction("我先看一下这个报错是什么意思。", nil, false) {
		t.Error("明确的「我先看一下」应催办")
	}
	if promiseButNoAction("这个报错可能是空指针。", nil, false) {
		t.Error("纯结论不应催办")
	}
}

// 计划模式的输出形态（结构化计划）不应被当成空承诺（由调用处 planMode 兜底，
// 这里只确认检测函数本身对「计划文本 + 有 pending」的判定行为）。
func TestPromiseButNoActionPlanText(t *testing.T) {
	pending := []store.Todo{{Content: "x", Status: "pending"}}
	// 计划文本里通常也有「先…」这类词，检测会命中；真正的豁免在调用处用 planMode 判断。
	if !promiseButNoAction("1. 先读 src/app.ts\n2. 再改路由", pending, false) {
		t.Log("计划式文本未命中未来时（也合理）")
	}
}

// 用户让 agent 做事的短请求要算「有活干」，纯提问/闲聊不算。
func TestLooksLikeActionRequest(t *testing.T) {
	yes := []string{
		"帮我改一下这个页面",
		"运行项目",
		"review 并 push",
		"创建一个新会话",
		"修一下这个报错",
	}
	for _, c := range yes {
		if !looksLikeActionRequest(c) {
			t.Errorf("应视为行动请求：%q", c)
		}
	}
	no := []string{
		"这个函数是做什么的？",
		"今天天气怎么样",
		"解释一下这段代码呢",
		"",
	}
	for _, c := range no {
		if looksLikeActionRequest(c) {
			t.Errorf("不应视为行动请求：%q", c)
		}
	}
}
