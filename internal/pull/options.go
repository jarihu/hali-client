package pull

// Options carries explicit download parameters for runPullWithOptions.
// Using a struct prevents global variable mutation when hali open delegates to the pull pipeline.
type Options struct {
	Repo           string
	Revision       string   // HF revision; "" resolves to "main" inside GetFiles
	FileName       string   // exact GGUF filename; "" means download all or select by index
	Files          []string // explicit subset of filenames to download (--files flag)
	NonInteractive bool
}
