package version

// Capabilities reports additive native interfaces, independently of release
// version strings. Installers must probe the actual paired binary.
func Capabilities() map[string]int {
	return map[string]int{"private_upload_context": 1, "session_scope_lock": 1}
}
