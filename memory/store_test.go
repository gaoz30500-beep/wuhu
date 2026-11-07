package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 临时自检：验证 append / 读取 / 检索 / 坏行容错 / ID 自增
func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory", "memory.jsonl")

	// 首次运行：文件不存在应返回空且不报错
	list, err := LoadAll(path)
	if err != nil || len(list) != 0 {
		t.Fatalf("首次读取应返回空,nil，实际 %v,%v", list, err)
	}

	r1, err := Append(path, "用户偏好用中文回答", "偏好,语言")
	if err != nil {
		t.Fatalf("Append 失败: %v", err)
	}
	if r1.ID != 1 {
		t.Fatalf("首条 ID 应为 1，实际 %d", r1.ID)
	}

	r2, _ := Append(path, "用户项目根目录是 E:\\laji\\项目\\AGENT", "项目")
	if r2.ID != 2 {
		t.Fatalf("第二条 ID 应为 2，实际 %d", r2.ID)
	}

	list, _ = LoadAll(path)
	if len(list) != 2 {
		t.Fatalf("应读到 2 条，实际 %d", len(list))
	}

	hits, _ := Search(path, "中文", 5)
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "中文") {
		t.Fatalf("检索\"中文\"应命中 1 条，实际 %d 条", len(hits))
	}

	// 空关键词 = 取最近几条，且从新到旧
	recent, _ := Search(path, "", 1)
	if len(recent) != 1 || recent[0].ID != 2 {
		t.Fatalf("空关键词应返回最新 1 条（ID=2），实际 %+v", recent)
	}

	// 坏行容错：追加一行垃圾后，其余仍可读
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("打开文件失败: %v", err)
	}
	f.WriteString("{这不是合法json\n")
	f.Close()

	list, err = LoadAll(path)
	if err != nil || len(list) != 2 {
		t.Fatalf("坏行应被跳过，仍读到 2 条，实际 %d,%v", len(list), err)
	}

	// 空内容必须被拒
	if _, err := Append(path, "   ", ""); err == nil {
		t.Fatalf("空内容应报错")
	}

	// Render 最近 2 条
	out := Render(path, 2)
	if !strings.Contains(out, "项目根目录") || !strings.HasPrefix(out, "- ") {
		t.Fatalf("Render 输出异常: %q", out)
	}
}
