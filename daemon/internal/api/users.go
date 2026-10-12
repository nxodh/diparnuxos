package api

import (
	"net/http"

	"github.com/nxodh/lightlixos/daemon/internal/accounts"
)

// accountStatus 는 계정 오류 코드를 HTTP 상태로 바꾼다.
func accountStatus(code string) int {
	switch code {
	case accounts.CodeInvalidUsername, accounts.CodeInvalidPassword:
		return http.StatusBadRequest
	case accounts.CodeUserExists:
		return http.StatusConflict
	case accounts.CodeUserNotFound:
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// accountNames 는 계정 이름 목록이다.
func accountNames(m AccountManager) ([]string, error) {
	list, err := m.List()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Name)
	}
	return names, nil
}

// usersView 는 GET /v1/users 의 응답이다.
type usersView struct {
	Users []accounts.Account `json:"users"`
}

// listUsers 는 Lightlix 로 만든 계정 목록을 돌려준다. 시스템 계정과 기본 live 계정은 포함하지 않는다.
func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	if s.d.Accounts == nil {
		writeJSON(w, http.StatusOK, usersView{Users: []accounts.Account{}})
		return
	}
	list, err := s.d.Accounts.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "accounts_unreadable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, usersView{Users: list})
}

// createUser 는 계정을 만든다. 본문은 {"name","password","admin"} 이다. 비밀번호는 응답이나 로그에 남기지 않는다.
// admin 이 true 이면 sudo 그룹에 넣는다. lightlix 그룹은 이미 시스템 관리 권한에 가까우므로 신뢰 경계는 같다.
func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if s.d.Accounts == nil {
		writeError(w, http.StatusInternalServerError, "internal", "account management is not configured")
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.d.Accounts.Add(r.Context(), body.Name, body.Password, body.Admin); err != nil {
		writeErr(w, err)
		return
	}
	created := ""
	if list, err := s.d.Accounts.List(); err == nil {
		for _, a := range list {
			if a.Name == body.Name {
				created = a.Created
			}
		}
	}
	writeJSON(w, http.StatusCreated, accounts.Account{Name: body.Name, Admin: body.Admin, Created: created})
}

// deleteUser 는 Lightlix 로 만든 계정을 지운다. 홈 디렉터리는 남긴다.
func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.d.Accounts == nil {
		writeError(w, http.StatusNotFound, "user_not_found", "account %q was not found", name)
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.d.Accounts.Remove(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true, "home_kept": true})
}
