package main

// Currentness belongs to the selected editing backend. An external EDL is not
// projected into the native DSL merely to reuse a renderer status flag.
func renderCurrent(p Project, r Render) bool {
	if r.Engine == "video_use" {
		return externalRenderCurrent(p, r)
	}
	if workflowFor(p).EditingBackend == "video_use" {
		return false
	}
	return r.ContentHash == productionHash(p) && r.SourceHash == narrationSourceHash(p)
}

func finalCurrent(p Project) bool {
	if p.Final == nil || p.Final.NarrationHash != narrationSourceHash(p) {
		return false
	}
	for _, r := range p.Renders {
		if r.ID == p.Final.RenderID {
			return r.State == "completed" && r.Range == nil && r.ContentHash == p.Final.ContentHash && renderCurrent(p, r)
		}
	}
	return false
}
