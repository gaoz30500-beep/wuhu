package MCP

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ShellIn struct {
	Command string `json:"command" jsonschema:"要在本机执行的 shell 命令，例如 dir、go version、git status"`
	Workdir string `json:"workdir,omitempty" jsonschema:"可选，命令运行的工作目录；不填用配置里的默认目录"`
	Timeout int64  `json:"timeout,omitempty" jsonschema:"可选，超时秒数；不填默认 30 秒"`
}

// ShellConfig 对应 mcp_config.json 里 tools.run_command 节点
type ShellConfig struct {
	Workdir string `json:"workdir,omitempty"`
	Timeout int64  `json:"timeout,omitempty"`
	LogPath string `json:"logpath,omitempty"`
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

	cmd := exec.CommandContext(ctx, "cmd", "/c", in.Command)
	if workdir != "" {
		cmd.Dir = workdir
	}

	out, err := cmd.CombinedOutput()
	output := string(out)
	text := fmt.Sprintf(
		"命令：%s\n输出：\n%s",
		in.Command, output,
	)

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
			text += "\n(命令超时，已终止！)"
		} else {
			status = "失败"
		}

		// 旁路记日志；写盘失败不阻断命令结果返回
		appendCommandLog(cfg.Tools.Shell.LogPath, in.Command, workdir, output, status, exitCode)

		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{
				Text: text + "\n" + detail,
			}},
		}, nil, nil
	}

	appendCommandLog(cfg.Tools.Shell.LogPath, in.Command, workdir, output, status, exitCode)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: text + "\n执行成功，退出码 0",
		}},
	}, nil, nil
}
