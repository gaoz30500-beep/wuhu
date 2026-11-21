package MCP

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type ShellIn struct {
	Command string `json:"command" jsonschema:"要在本机执行的 shell 命令，例如 dir、go version、git status"`
	Workdir string `json:"workdir,omitempty" jsonschema:"可选，命令运行的工作目录；不填用配置里的默认目录"`
	Timeout int64  `json:"timeout,omitempty" jsonschema:"可选，超时秒数；不填默认 30 秒"`
}

// ShellConfig 对应 mcp_config.json 里 tools.run_command 节点
type ShellConfig struct {
	Workdir     string `json:"workdir,omitempty"`
	Timeout     int64  `json:"timeout,omitempty"`
	LogPath     string `json:"logpath,omitempty"`
	Shell       string `json:"shell,omitempty"`       // 可选，cmd（默认）或 powershell：powershell 模式下命令按 PowerShell 脚本执行
	MaxOutputKB int64  `json:"maxOutputKB,omitempty"` // 可选，单次返回给模型的最大输出（KB），默认 32
}

func registerShellInto(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_command",
		Description: "在本机执行一条 shell 命令，返回标准输出、错误输出和退出码。" +
			"只读类操作（查看目录、查看版本、查询状态等）可直接执行；" +
			"涉及文件或目录的新建、修改、删除等改动类命令，必须先向用户说明会改变什么，并在用户确认后再执行；" +
			"命令失败时，根据返回的 stderr 和退出码调整参数后再试。",
	}, runShellCommand)
}

// defaultCommandLogPath 命令日志默认落盘位置；配置没给 logPath 时用它
const defaultCommandLogPath = "./out/command.log"

// defaultMaxOutputKB 单次返回给模型的输出上限（KB）。
// 一条 dir /s 能吐出几 MB 文本，全塞回上下文就是灾难，超了就截断并注明。
const defaultMaxOutputKB = 32

// progressGuard 抑制 PowerShell 的进度流。
// 一旦 stdout/stderr 被重定向（工具调用必然是重定向的），PowerShell 会把进度信息
// 序列化成 CLIXML 混进输出，正文前面多一坨 "#< CLIXML ..."，既难读又费 token。
const progressGuard = "$ProgressPreference='SilentlyContinue';"

// appendCommandLog 把一次命令执行的旁路记录追加写进日志文件。
// 这是排障/审计用的旁路：写盘失败只打到 stderr，不影响命令本身的返回结果。
func appendCommandLog(logPath, command, workdir, output, status string, exitCode int) {
	if logPath == "" {
		logPath = defaultCommandLogPath
	}

	// 父目录不存在先逐级创建（out 文件夹可能还没建）
	if dir := filepath.Dir(logPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("命令日志：创建目录 %s 失败: %v", dir, err)
			return
		}
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("命令日志：打开 %s 失败: %v", logPath, err)
		return
	}
	defer f.Close()

	record := fmt.Sprintf(
		"[%s] 状态：%s | 退出码：%d | 目录：%s\n命令：%s\n输出：\n%s\n------------------------------------------------------------\n",
		time.Now().Format("2006-01-02 15:04:05"),
		status, exitCode, workdir, command, output,
	)

	if _, err := f.WriteString(record); err != nil {
		log.Printf("命令日志：写入 %s 失败: %v", logPath, err)
	}
}

// decodeConsoleOutput 把 Windows 控制台输出转成 UTF-8。
//
// 中文 Windows 上 cmd 的内置命令（dir、type、fsutil 等）按代码页 936（GBK）输出，
// 而 Go 这边是 UTF-8 世界：直接 string(out) 会让所有中文变成乱码。
// 已经是合法 UTF-8 的（比如自己设了 chcp 65001 的程序）原样返回，不重复解码。
func decodeConsoleOutput(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil {
		return string(out)
	}
	return string(b) // 解不动就原样给，至少别丢信息
}

// indexToken 在 s 里找独立的 token（前后必须是空白或串首尾）。
// 直接 strings.Index 会把 "-c" 匹配到 "-command" 里面去。
func indexToken(s, token string) int {
	for off := 0; off < len(s); {
		i := strings.Index(s[off:], token)
		if i < 0 {
			return -1
		}
		i += off
		before := i == 0 || s[i-1] == ' ' || s[i-1] == '\t'
		afterIdx := i + len(token)
		after := afterIdx >= len(s) || s[afterIdx] == ' ' || s[afterIdx] == '\t'
		if before && after {
			return i
		}
		off = afterIdx
	}
	return -1
}

// trimOuterQuote 去掉脚本最外层的一对引号（单双都去）。
// 多层 shell 传递时引号经常被保留或换成单引号，PowerShell 收到带引号的整串会
// 把它当字符串**原样输出**——这就是"命令被回显但不执行"的根因。
func trimOuterQuote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// utf16leBytes PowerShell 的 -EncodedCommand 要求 UTF-16LE 字节
func utf16leBytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return buf
}

// encodePowerShell 把一段脚本包成 `powershell -EncodedCommand <base64>`
func encodePowerShell(script string) string {
	if !strings.Contains(script, "ProgressPreference") {
		script = progressGuard + " " + script
	}
	return base64.StdEncoding.EncodeToString(utf16leBytes(script))
}

// prepareCommand 把 `powershell ... -Command "脚本"` 改写成 `-EncodedCommand <base64>`。
//
// 为什么必须这么办：实测 `cmd /c powershell -Command "Write-Output (Get-Date).Year"`
// 拿到的输出就是脚本原文本身——引号被 cmd 保留着传进去，PowerShell 把带引号的
// 整串当字符串打印了，压根没执行。base64 里只有字母数字和 +/=，穿几层都不变形。
func prepareCommand(command string) (string, bool) {
	low := strings.ToLower(command)

	// 定位 powershell/pwsh 可执行名（可能带完整路径）
	kw, kwIdx := "", -1
	if i := strings.LastIndex(low, "powershell"); i >= 0 {
		kw, kwIdx = "powershell", i
	}
	if i := strings.LastIndex(low, "pwsh"); i >= 0 && i > kwIdx {
		kw, kwIdx = "pwsh", i
	}
	if kwIdx < 0 {
		return command, false
	}

	exeEnd := kwIdx + len(kw)
	if strings.HasPrefix(low[exeEnd:], ".exe") {
		exeEnd += 4
	}
	head := command[:exeEnd] // 含路径的 exe 名
	rest := command[exeEnd:] // 参数部分

	// 形如 "C:\Program Files\...\pwsh.exe" -Command ...：结尾引号落在 rest 开头
	if strings.HasPrefix(rest, `"`) || strings.HasPrefix(rest, `'`) {
		rest = rest[1:]
	}

	lowRest := strings.ToLower(rest)
	flagIdx, flagLen := -1, 0
	if i := indexToken(lowRest, "-command"); i >= 0 {
		flagIdx, flagLen = i, len("-command")
	} else if i := indexToken(lowRest, "-c"); i >= 0 {
		flagIdx, flagLen = i, len("-c")
	}
	if flagIdx < 0 {
		return command, false
	}

	args := strings.TrimSpace(rest[:flagIdx])
	script := trimOuterQuote(strings.TrimSpace(rest[flagIdx+flagLen:]))
	if script == "" {
		return command, false
	}
	// 已经是编码形式，别套娃
	if strings.Contains(strings.ToLower(script), "-encodedcommand") {
		return command, false
	}

	out := head
	// 统一补 -NoProfile：profile 脚本可能往 stdout 打欢迎语，污染工具结果
	if !strings.Contains(strings.ToLower(args), "-noprofile") {
		out += " -NoProfile"
	}
	if args != "" {
		out += " " + args
	}
	return out + " -EncodedCommand " + encodePowerShell(script), true
}

// clipOutput 按字节上限截断输出，截断点落在 UTF-8 字符边界上
func clipOutput(s string, maxBytes int64) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxOutputKB * 1024
	}
	if int64(len(s)) <= maxBytes {
		return s, false
	}
	cut := int(maxBytes)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf(
		"\n…（输出 %d 字节，超过 %d KB 上限已截断；需要看后面部分请把命令改窄，例如加 findstr 过滤、或重定向到文件后再用 read_file 分页读取）",
		len(s), maxBytes/1024,
	), true
}

func runShellCommand(ctx context.Context, req *mcp.CallToolRequest, in ShellIn) (*mcp.CallToolResult, any, error) {
	workdir := in.Workdir
	if workdir == "" {
		workdir = cfg.Tools.Shell.Workdir
	}
	timeout := time.Duration(in.Timeout) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(cfg.Tools.Shell.Timeout) * time.Second
	}
	if timeout <= 0 {
		timeout = time.Duration(30) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := strings.ToLower(strings.TrimSpace(cfg.Tools.Shell.Shell))
	var cmd *exec.Cmd
	rewritten := false
	switch shell {
	case "powershell", "pwsh":
		// 配置强制 PowerShell 模式：整条命令按脚本执行，本来就不经过 cmd 的引号解析
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive",
			"-EncodedCommand", encodePowerShell(in.Command))
	default:
		// 命令改写：PowerShell 的 -Command 转 -EncodedCommand，绕开引号被吃的问题
		execCommand, ok := prepareCommand(in.Command)
		rewritten = ok
		cmd = exec.CommandContext(ctx, "cmd", "/c", execCommand)
	}
	if workdir != "" {
		cmd.Dir = workdir
	}

	// 分开收 stdout / stderr：合并成一个流之后，一旦命令出错，
	// 报错信息会和正常输出搅在一起，模型分不清哪句是结果、哪句是抱怨。
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()

	stdout := decodeConsoleOutput(stdoutBuf.Bytes())
	stderr := decodeConsoleOutput(stderrBuf.Bytes())

	var sb strings.Builder
	fmt.Fprintf(&sb, "命令：%s\n输出：\n%s", in.Command, stdout)
	if strings.TrimSpace(stderr) != "" {
		fmt.Fprintf(&sb, "\n错误输出：\n%s", stderr)
	}

	maxOutput := cfg.Tools.Shell.MaxOutputKB
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputKB
	}
	text, clipped := clipOutput(sb.String(), maxOutput*1024)
	if rewritten {
		text += "\n（已把 PowerShell 脚本转为 -EncodedCommand 执行，避免引号在 shell 传递中被吃掉）"
	}
	if clipped {
		text += "\n（输出已按上限截断）"
	}

	logOutput := stdout
	if logOutput == "" {
		logOutput = stderr
	}

	exitCode := 0
	status := "成功"

	if err != nil {
		exitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
		detail := fmt.Sprintf("执行失败，退出码：%d，错误：%v", exitCode, err)
		if ctx.Err() == context.DeadlineExceeded {
			status = "超时"
			text += fmt.Sprintf("\n(命令超时：超过 %s 已终止！可调大 timeout，或把命令拆小)", timeout)
		} else {
			status = "失败"
		}

		// 旁路记日志；写盘失败不阻断命令结果返回
		appendCommandLog(cfg.Tools.Shell.LogPath, in.Command, workdir, logOutput, status, exitCode)

		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{
				Text: text + "\n" + detail,
			}},
		}, nil, nil
	}

	appendCommandLog(cfg.Tools.Shell.LogPath, in.Command, workdir, logOutput, status, exitCode)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: text + "\n执行成功，退出码 0",
		}},
	}, nil, nil
}
