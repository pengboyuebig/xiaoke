// 后台任务管理接口：页面上「运行中任务」面板用。
// 这些任务是 run_command is_background 起的真实进程，关闭时按进程树终止
// （Windows 上 npm.cmd 会派生 node 子进程，只杀父进程端口不会释放）。
package api

import (
	"net/http"

	"xiaoke/internal/tools"
)

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tools.ListBackgroundTasks()})
}

func (h *Handler) taskDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, output, ok := tools.TaskDetail(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在（可能已被清理）"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": info, "output": output})
}

func (h *Handler) killTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := tools.KillBackgroundTask(id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}
