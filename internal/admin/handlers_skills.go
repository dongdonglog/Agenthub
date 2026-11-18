package admin

import (
	"io"
	"net/http"
	"strings"
)

func (s *Server) handleSkills(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
		return
	}
	items, err := s.store.ListSkills()
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if s.opts.Profiles != nil {
		if profiles, err := s.opts.Profiles.ListProfiles(); err == nil {
			counts := make(map[string]int)
			for _, p := range profiles {
				for _, name := range p.Skills {
					counts[name]++
				}
			}
			for i := range items {
				items[i].References = counts[items[i].Name]
			}
		}
	}
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
}

func (s *Server) handleSkill(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodGet:
		item, err := s.store.GetSkill(name)
		if err != nil {
			writeError(rw, http.StatusNotFound, "技能不存在")
			return
		}
		writeJSON(rw, http.StatusOK, item)
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			writeError(rw, http.StatusBadRequest, "内容不能为空")
			return
		}
		if err := s.store.PutSkill(name, body.Content); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("skill saved", "name", name)
		s.audit(r, "skill.save", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.store.DeleteSkill(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("skill deleted", "name", name)
		s.audit(r, "skill.delete", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleSkillImport(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeError(rw, http.StatusBadRequest, "需要 multipart/form-data")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(rw, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 20<<20))
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "1"
	names, err := s.store.ImportZip(data, overwrite)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "skill.import", strings.Join(names, ","), "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "imported": names})
}

func (s *Server) handleSkillFiles(rw http.ResponseWriter, r *http.Request) {
	files, err := s.store.ListFiles(r.PathValue("name"))
	if err != nil {
		writeError(rw, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": files, "total": len(files)})
}

func (s *Server) handleSkillFile(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rel := r.PathValue("path")
	switch r.Method {
	case http.MethodGet:
		content, err := s.store.ReadFile(name, rel)
		if err != nil {
			writeError(rw, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(rw, http.StatusOK, map[string]string{"path": rel, "content": content})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.WriteFile(name, rel, body.Content); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.audit(r, "skill.file.save", name, rel)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	case http.MethodDelete:
		if err := s.store.DeleteFile(name, rel); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.audit(r, "skill.file.delete", name, rel)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleSkillStatus(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Status string `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetSkillStatus(name, body.Status); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("skill status changed", "name", name, "status", body.Status)
	s.audit(r, "skill.status", name, body.Status)
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
}

func (s *Server) handleMCPs(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
		return
	}
	items, err := s.store.ListMCPs()
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
}

func (s *Server) handleMCP(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodPut:
		var item MCP
		if err := decode(r, &item); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		item.Name = name
		if item.Transport == "" {
			item.Transport = "stdio"
		}
		if err := s.store.PutMCP(item); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("mcp saved", "name", name)
		if s.opts.ReloadAgentConfig != nil {
			if err := s.opts.ReloadAgentConfig(); err != nil {
				s.log.Warn("reload agent config failed", "error", err)
			}
		}
		s.audit(r, "mcp.save", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.store.DeleteMCP(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("mcp deleted", "name", name)
		if s.opts.ReloadAgentConfig != nil {
			if err := s.opts.ReloadAgentConfig(); err != nil {
				s.log.Warn("reload agent config failed", "error", err)
			}
		}
		s.audit(r, "mcp.delete", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}
