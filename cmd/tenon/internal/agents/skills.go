package agents

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// skillFiles 是框架的技能：skills/<名称>/SKILL.md，开放的 Agent Skills 格式（YAML 头的 name、description 加 Markdown）。
// 技能写“做某件事的步骤和要点”，细节链接到 .tenon/docs 中的文档页，助手按 description 决定什么时候加载它。
//
//go:embed skills
var skillFiles embed.FS

// SkillPrefix 是框架技能的名称前缀：tenon agents 只管理以它开头的技能目录，项目自己的技能保持原样。
const SkillPrefix = "tenon-"

// Skill 是一个技能。
type Skill struct {
	Name        string // 技能名，也是安装后的目录名
	Description string // 什么时候用它
	Content     []byte // SKILL.md 的全部内容
}

// conditions 是只在部分项目中安装的技能和安装的条件；其余技能每个项目都安装。
var conditions = map[string]func(p *Project) bool{
	"tenon-frontend": func(p *Project) bool { return len(p.Config.Frontends) > 0 },
	"tenon-electron": func(p *Project) bool {
		return slices.ContainsFunc(p.Config.Clients, func(c project.Client) bool { return c.Kind == "electron" })
	},
}

// AllSkills 返回框架的全部技能，按名称排序。SKILL.md 缺少 name、description，或者 name 与目录名不同时返回错误。
func AllSkills() ([]Skill, error) {
	entries, err := fs.ReadDir(skillFiles, "skills")
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		data, err := skillFiles.ReadFile(path.Join("skills", e.Name(), "SKILL.md"))
		if err != nil {
			return nil, err
		}
		s, err := parseSkill(data)
		if err != nil {
			return nil, fmt.Errorf("skills/%s/SKILL.md: %w", e.Name(), err)
		}
		if s.Name != e.Name() || !strings.HasPrefix(s.Name, SkillPrefix) {
			return nil, fmt.Errorf("skills/%s/SKILL.md: name %q must be the directory name and start with %s", e.Name(), s.Name, SkillPrefix)
		}
		skills = append(skills, s)
	}
	return skills, nil
}

// parseSkill 读出 SKILL.md 开头 --- 之间的 name 和 description。
func parseSkill(data []byte) (Skill, error) {
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	head, _, found := bytes.Cut(rest, []byte("\n---\n"))
	if !ok || !found {
		return Skill{}, fmt.Errorf("no front matter between --- lines")
	}
	s := Skill{Content: data}
	for line := range strings.Lines(string(head)) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), ":")
		switch key {
		case "name":
			s.Name = strings.TrimSpace(value)
		case "description":
			s.Description = strings.TrimSpace(value)
		}
	}
	if s.Name == "" || s.Description == "" {
		return Skill{}, fmt.Errorf("front matter needs name and description")
	}
	return s, nil
}

// Skills 返回项目 p 要安装的技能：满足 conditions 中的条件，且不在 tenon.json 的 agents.skip 中。
// skip 中有框架没有的技能时返回错误。
func Skills(p *Project) ([]Skill, error) {
	all, err := AllSkills()
	if err != nil {
		return nil, err
	}
	var skip []string
	if p.Config.Agents != nil {
		skip = p.Config.Agents.Skip
	}
	for _, name := range skip {
		if !slices.ContainsFunc(all, func(s Skill) bool { return s.Name == name }) {
			return nil, fmt.Errorf("%s: agents.skip: %q is not a framework skill", project.File, name)
		}
	}
	var skills []Skill
	for _, s := range all {
		if slices.Contains(skip, s.Name) {
			continue
		}
		if cond, ok := conditions[s.Name]; ok && !cond(p) {
			continue
		}
		skills = append(skills, s)
	}
	return skills, nil
}

// SkillDirs 返回 targets 中的助手读取技能的目录：Claude Code 读 .claude/skills，Codex 和 Antigravity 读 .agents/skills，
// GitHub Copilot 读 .github/skills；Cursor 读 .claude/skills 和 .agents/skills，所以没有 Claude Code 时才为它写 .agents/skills。
func SkillDirs(targets []string) []string {
	has := func(t string) bool { return slices.Contains(targets, t) }
	var dirs []string
	if has("claude") {
		dirs = append(dirs, ".claude/skills")
	}
	if has("codex") || has("antigravity") || has("cursor") && !has("claude") {
		dirs = append(dirs, ".agents/skills")
	}
	if has("copilot") {
		dirs = append(dirs, ".github/skills")
	}
	return dirs
}

// WriteSkills 把 skills 写进项目 dir 中的每个 dirs：先删除其中以 SkillPrefix 开头的目录（框架删掉或项目跳过的技能
// 随之消失），再写入 skills。其他技能保持原样。
func WriteSkills(dir string, dirs []string, skills []Skill) error {
	for _, d := range dirs {
		root := filepath.Join(dir, filepath.FromSlash(d))
		entries, err := os.ReadDir(root)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), SkillPrefix) {
				if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
					return err
				}
			}
		}
		for _, s := range skills {
			if err := os.MkdirAll(filepath.Join(root, s.Name), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(root, s.Name, "SKILL.md"), s.Content, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
