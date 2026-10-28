package muse

import "pp/muse-mcp/internal/musepage"

// agentTexts reads the agent messages on a chat's open tab.
func (s *Service) agentTexts(chatID string) []string {
	s.mu.Lock()
	t := s.tabs[chatID]
	s.mu.Unlock()
	if t == nil {
		return nil
	}
	st, err := musepage.Snapshot(t.ctx)
	if err != nil {
		return nil
	}
	return st.AgentTexts
}
