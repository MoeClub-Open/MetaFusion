package curator

func (s *Service) enabledCodes(settings Settings) []string {
	codes := []string{CodeManualTask}
	if settings.Execution.AllowReviews {
		codes = append(codes, CodeReview)
	}
	for _, detector := range s.enabledDetectors(settings) {
		codes = append(codes, detector.code)
	}
	if settings.AllowStructures {
		codes = append(codes, CodeDanglingReference, CodeMissingRequiredField,
			CodeReleaseMissingMedia, CodeMediumMissingTracks, CodeTrackEmptyContents, CodeTrackPositionGap)
	}
	return codes
}

func (s *Service) findingEnabled(settings Settings, code string) bool {
	for _, enabled := range s.enabledCodes(settings) {
		if code == enabled {
			return true
		}
	}
	return false
}
