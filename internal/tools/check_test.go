package tools

import "testing"

func TestIsWriteCapableMCPNames(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// filesystem server 的真实工具名
		{"read_file", false},
		{"read_multiple_files", false},
		{"list_directory", false},
		{"directory_tree", false},
		{"search_files", false},
		{"get_file_info", false},
		{"list_allowed_directories", false},
		{"write_file", true},
		{"edit_file", true},
		{"create_directory", true},
		{"move_file", true},
		{"delete_file", true},
		// git server
		{"git_status", false},
		{"git_log", false},
		{"git_commit", true},
		{"git_push", true},
		{"git_reset", true},
		// 只读工具不得被新关键词误伤
		{"list_files", false},
		{"find_files", false},
		{"read_file", false},
		{"search_files", false},
		{"web_fetch", false},
		{"skill_read", false},
		{"memory_read", false},
		{"task_output", false},
		{"git_diff", false},
		{"git_show", false},
		{"git_blame", false},
		{"describe_branch", false},
		{"list_tags", false},
	}
	for _, c := range cases {
		got := IsWriteCapable(c.name)
		if got != c.want {
			t.Errorf("IsWriteCapable(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
