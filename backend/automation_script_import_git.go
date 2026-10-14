package backend

import (
	"ant-chrome/backend/internal/upstream"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"ant-chrome/backend/internal/automation"
)

func cloneAutomationGitRepository(repoURL string, ref string) (string, func(), error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", nil, fmt.Errorf("未找到 git，可先安装 git 后再导入仓库脚本")
	}

	tempDir, err := os.MkdirTemp("", "ant-automation-git-*")
	if err != nil {
		return "", nil, fmt.Errorf("创建 Git 临时目录失败: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	if strings.TrimSpace(ref) == "" {
		if err := runGitCommand("", "clone", "--depth", "1", repoURL, tempDir); err != nil {
			cleanup()
			return "", nil, err
		}
		return tempDir, cleanup, nil
	}

	if err := runGitCommand("", "clone", "--depth", "1", "--branch", ref, "--single-branch", repoURL, tempDir); err == nil {
		return tempDir, cleanup, nil
	}

	_ = os.RemoveAll(tempDir)
	tempDir, err = os.MkdirTemp("", "ant-automation-git-*")
	if err != nil {
		return "", nil, fmt.Errorf("创建 Git 临时目录失败: %w", err)
	}
	cleanup = func() {
		_ = os.RemoveAll(tempDir)
	}

	if err := runGitCommand("", "clone", repoURL, tempDir); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := runGitCommand(tempDir, "checkout", ref); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("切换 Git 引用失败: %w", err)
	}
	return tempDir, cleanup, nil
}

// gitUpstreamArgs 启用前置代理时让 git 的 HTTP(S) 传输经前置代理；SSH 地址无法经前置代理，直接拒绝。
func gitUpstreamArgs(args []string) ([]string, error) {
	if err := upstream.ConfigError(); err != nil {
		return nil, err
	}
	p := upstream.Current()
	if p == nil {
		return args, nil
	}
	if len(args) > 1 && args[0] == "clone" {
		repo := strings.ToLower(strings.TrimSpace(args[1]))
		if !strings.HasPrefix(repo, "https://") && !strings.HasPrefix(repo, "http://") {
			return nil, fmt.Errorf("已启用前置代理，Git 仓库仅支持 https:// 地址（SSH 地址无法经前置代理）")
		}
	}
	return append([]string{"-c", "http.proxy=" + p.ToolURL()}, args...), nil
}

func runGitCommand(workdir string, args ...string) error {
	gitArgs, err := gitUpstreamArgs(args)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", gitArgs...)
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("git %s 失败: %s", strings.Join(args, " "), message)
	}
	return nil
}

func (a *App) loadAutomationGitBundle(repoURL string, ref string, scriptPath string) (automation.ImportedBundle, error) {
	normalizedRepoURL := strings.TrimSpace(repoURL)
	if normalizedRepoURL == "" {
		return automation.ImportedBundle{}, fmt.Errorf("Git 仓库地址不能为空")
	}

	normalizedRef := strings.TrimSpace(ref)
	normalizedScriptPath := strings.TrimSpace(scriptPath)

	repoDir, cleanup, err := cloneAutomationGitRepository(normalizedRepoURL, normalizedRef)
	if err != nil {
		return automation.ImportedBundle{}, err
	}
	defer cleanup()

	bundle, err := automation.ImportBundleFromDirectoryWithOptions(repoDir, normalizedScriptPath, buildAutomationGitImportLabel(normalizedRepoURL, normalizedRef, normalizedScriptPath), a.automationScriptImportOptions())
	if err != nil {
		return automation.ImportedBundle{}, err
	}
	return bundle, nil
}
