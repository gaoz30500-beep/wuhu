package memory

import (
	"path/filepath"
	"testing"
)

func TestList按关键词过滤(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")

	if _, err := Write(path, "用户的显示器是 4K 的", "设备,显示器", "tool"); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(path, "用户偏好用中文回答", "偏好,语言", "tool"); err != nil {
		t.Fatal(err)
	}

	all, err := List(path, "", 0)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("应有 2 条，实际 %d 条", len(all))
	}

	hits, err := List(path, "显示器", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("关键词过滤后应剩 1 条，实际 %d 条", len(hits))
	}

	limit2, err := List(path, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limit2) != 1 {
		t.Fatalf("limit=1 应返回 1 条，实际 %d 条", len(limit2))
	}
}

func TestUpdate与Delete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")

	res, err := Write(path, "用户用的是 1080p 显示器", "设备", "tool")
	if err != nil {
		t.Fatal(err)
	}
	id := res.Record.ID

	// 改正文：指纹要跟着重算
	updated, err := Update(path, id, func(r *Record) {
		r.Content = "用户用的是 4K 显示器"
	})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if updated.Finger != Fingerprint("用户用的是 4K 显示器") {
		t.Fatal("正文改了，指纹应重算")
	}

	// 正文不许清空
	if _, err := Update(path, id, func(r *Record) { r.Content = "  " }); err == nil {
		t.Fatal("把正文清空应当报错")
	}

	n, err := Delete(path, id)
	if err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应删掉 1 条，实际 %d 条", n)
	}
	left, err := List(path, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("删完应为空，实际还剩 %d 条", len(left))
	}
	// 删不存在的 ID：不报错，返回 0
	if n, err := Delete(path, 999); err != nil || n != 0 {
		t.Fatalf("删不存在的 ID 应返回 (0, nil)，实际 (%d, %v)", n, err)
	}
}
