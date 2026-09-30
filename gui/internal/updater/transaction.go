package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Plan struct {
	Token     string `json:"token"`
	Version   string `json:"version"`
	Install   string `json:"install"`
	Work      string `json:"work"`
	ParentPID int    `json:"parentPID"`
	Files     []File `json:"files"`
}

type Result struct {
	Version string `json:"version"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Time    int64  `json:"time"`
}
type Change struct {
	Name    string `json:"name"`
	Existed bool   `json:"existed"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
}
type Journal struct {
	Plan    Plan     `json:"plan"`
	Changes []Change `json:"changes"`
}

func WriteJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(temp, path)
}

func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) > 2<<20 {
		return fmt.Errorf("更新状态文件过大")
	}
	return json.Unmarshal(b, v)
}
func (p Plan) Stage() string       { return filepath.Join(p.Work, "stage") }
func (p Plan) Backup() string      { return filepath.Join(p.Install, ".ez-update-backup-"+p.Token) }
func (p Plan) JournalPath() string { return filepath.Join(p.Backup(), "journal.json") }
func (p Plan) ResultPath() string  { return filepath.Join(p.Install, ".ez-update-result.json") }

func (p Plan) Validate() error {
	if len(p.Token) != 32 || strings.Trim(p.Token, "0123456789abcdef") != "" || len(p.Files) == 0 || ParseVersion(p.Version) == nil {
		return fmt.Errorf("更新计划无效")
	}
	for _, root := range []string{p.Install, p.Work} {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Dir(root) == root {
			return fmt.Errorf("更新目录无效")
		}
		if _, err := safePath(root, "scrcpy-ez.exe"); err != nil {
			return err
		}
	}
	if strings.EqualFold(p.Install, p.Work) {
		return fmt.Errorf("暂存目录不能是安装目录")
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if protected(f.Name) || seen[strings.ToLower(f.Name)] {
			return fmt.Errorf("更新计划文件无效")
		}
		seen[strings.ToLower(f.Name)] = true
		stage, err := safePath(p.Stage(), f.Name)
		if err != nil {
			return err
		}
		if _, err := safePath(p.Install, f.Name); err != nil {
			return err
		}
		h, size, err := fileHash(stage)
		if err != nil {
			return err
		}
		if h != f.SHA256 || size != f.Size {
			return fmt.Errorf("暂存文件校验失败：%s", f.Name)
		}
	}
	for _, name := range requiredFiles {
		if !seen[strings.ToLower(name)] {
			return fmt.Errorf("更新计划缺少 %s", name)
		}
	}
	v, err := FileVersion(filepath.Join(p.Stage(), "scrcpy-ez.exe"))
	if err != nil {
		return err
	}
	if strings.TrimPrefix(v, "v") != strings.TrimPrefix(p.Version, "v") {
		return fmt.Errorf("暂存程序版本不匹配")
	}
	return nil
}

func CheckWritable(root string) error {
	f, err := os.CreateTemp(root, ".ez-write-*")
	if err != nil {
		return fmt.Errorf("当前目录无法写入，请将 ez 移到可写文件夹后重试：%w", err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func Apply(p Plan) (j Journal, err error) {
	if err = p.Validate(); err != nil {
		return j, err
	}
	if err = CheckWritable(p.Install); err != nil {
		return j, err
	}
	if err = os.Mkdir(p.Backup(), 0700); err != nil {
		return j, fmt.Errorf("创建更新备份失败：%w", err)
	}
	j.Plan = p
	if err = WriteJSON(p.JournalPath(), j); err != nil {
		return j, err
	}
	for _, f := range p.Files {
		dest, e := safePath(p.Install, f.Name)
		if e != nil {
			return j, e
		}
		backup, e := safePath(filepath.Join(p.Backup(), "files"), f.Name)
		if e != nil {
			return j, e
		}
		st, e := os.Lstat(dest)
		if e != nil && !os.IsNotExist(e) {
			return j, e
		}
		if e == nil && !st.Mode().IsRegular() {
			return j, fmt.Errorf("目标不是普通文件：%s", f.Name)
		}
		change := Change{Name: f.Name, Existed: e == nil}
		if change.Existed {
			if e = copyFile(dest, backup); e != nil {
				return j, e
			}
			change.SHA256, change.Size, e = fileHash(backup)
			if e != nil {
				return j, e
			}
		}
		// 完整写入并同步临时文件，再原子替换；中断不会留下半个 EXE。
		temp := filepath.Join(filepath.Dir(dest), ".ez-update-new-"+p.Token)
		if _, e = safePath(p.Install, strings.TrimPrefix(temp, p.Install+string(filepath.Separator))); e != nil {
			return j, e
		}
		_ = os.Remove(temp)
		if e = copyFile(filepath.Join(p.Stage(), f.Name), temp); e != nil {
			_ = os.Remove(temp)
			return j, e
		}
		j.Changes = append(j.Changes, change)
		if e = WriteJSON(p.JournalPath(), j); e == nil {
			e = replaceFile(temp, dest)
		}
		_ = os.Remove(temp)
		if e != nil {
			return j, fmt.Errorf("文件仍被占用或不可写：%s：%w", f.Name, e)
		}
	}
	return j, nil
}

func Rollback(j Journal) error {
	var errors []string
	for i := len(j.Changes) - 1; i >= 0; i-- {
		c := j.Changes[i]
		dest, err := safePath(j.Plan.Install, c.Name)
		if err != nil {
			return err
		}
		backup, err := safePath(filepath.Join(j.Plan.Backup(), "files"), c.Name)
		if err != nil {
			return err
		}
		if c.Existed {
			if _, err := os.Lstat(backup); os.IsNotExist(err) {
				h, n, e := fileHash(dest)
				if e == nil && c.SHA256 != "" && h == c.SHA256 && n == c.Size {
					continue
				}
				errors = append(errors, "缺少旧文件备份："+c.Name)
				continue
			} else if err != nil {
				errors = append(errors, err.Error())
				continue
			}
			// 替换未发生或此前已恢复的文件无需再次删除，亦兼容仍被占用的旧文件。
			oldHash, oldSize, e := fileHash(backup)
			if e != nil {
				errors = append(errors, e.Error())
				continue
			}
			if oldHash != c.SHA256 || oldSize != c.Size {
				errors = append(errors, "旧文件备份校验失败："+c.Name)
				continue
			}
			currentHash, currentSize, currentErr := fileHash(dest)
			if e == nil && currentErr == nil && oldSize == currentSize && oldHash == currentHash {
				continue
			}
		}
		if c.Existed {
			if err := replaceFile(backup, dest); err != nil {
				errors = append(errors, err.Error())
			}
		} else if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			errors = append(errors, err.Error())
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("恢复旧版失败，备份位于 %s：%s", j.Plan.Backup(), strings.Join(errors, "; "))
	}
	return nil
}

func SaveResult(p Plan, ok bool, err error) error {
	r := Result{Version: p.Version, OK: ok, Time: time.Now().Unix()}
	if err != nil {
		r.Error = err.Error()
	}
	return WriteJSON(p.ResultPath(), r)
}
