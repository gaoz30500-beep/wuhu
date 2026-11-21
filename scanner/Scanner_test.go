package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOptionsMatchExt(t *testing.T) {
	o := Options{Extensions: []string{"log", ".TMP"}}
	cases := map[string]bool{
		"a.LOG":     true,
		"b.tmp":     true,
		"c.txt":     false,
		"noext":     false,
		"d.log.bak": false,
	}
	for name, want := range cases {
		if got := o.matchExt(name); got != want {
			t.Fatalf("%s: 期望 %v，实际 %v", name, want, got)
		}
	}
	// 名单为空 = 全部通过
	if !(Options{}).matchExt("anything.bin") {
		t.Fatal("未配置扩展名时应全部通过")
	}
}

func TestScan过滤生效(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-200 * 24 * time.Hour)

	write := func(rel string, size int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		// 把 atime 拨回 200 天前，制造"闲置"文件
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	write("keep.log", 2048)
	write("skip.txt", 2048)             // 扩展名不在白名单
	write("small.log", 10)              // 体积不够
	write("node_modules/dep.log", 4096) // 目录被排除

	// idleDays=1 是为了让"刚拨回去 200 天"的文件必然入选
	opts := Options{Extensions: []string{"log"}, MinSize: 1024, ExcludeDirs: []string{"node_modules"}}
	list, err := NewScannerWithOptions(root, 1, opts).Scan()
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(list) != 1 {
		paths := make([]string, 0, len(list))
		for _, r := range list {
			paths = append(paths, r.Path)
		}
		t.Fatalf("应只剩 1 个文件，实际 %d 个: %v", len(list), paths)
	}
	if filepath.Base(list[0].Path) != "keep.log" {
		t.Fatalf("留下的应该是 keep.log，实际 %s", list[0].Path)
	}
}

func TestScan条数上限(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-200 * 24 * time.Hour)
	for _, n := range []string{"a.log", "b.log", "c.log", "d.log"} {
		p := filepath.Join(root, n)
		if err := os.WriteFile(p, make([]byte, 8), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	list, err := NewScannerWithOptions(root, 1, Options{MaxResults: 2}).Scan()
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("上限 2 应只返回 2 条，实际 %d 条", len(list))
	}
}
