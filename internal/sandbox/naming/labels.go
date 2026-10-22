package naming

// Labels the launcher attaches to the project sandbox VM at creation. The
// org.agents-sandbox. prefix is shared with the runner-image labels.
const (
	// LabelProject identifies the project the sandbox belongs to.
	LabelProject = "org.agents-sandbox.project"
	// LabelImage records the runner image reference the sandbox was created with.
	LabelImage = "org.agents-sandbox.image"
	// LabelTask marks a transient task sandbox, so it is kept out of the
	// project-VM listing and is always reclaimable by the pruner.
	LabelTask = "org.agents-sandbox.task"
)
