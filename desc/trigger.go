package desc

// Trigger represents a database trigger.
type Trigger struct {
	Catalog           string // Catalog name of the trigger
	SearchPath        string // Search path of the trigger
	Name              string // Name of the trigger
	Manipulation      string // Type of manipulation (INSERT, UPDATE, DELETE)
	TableName         string // Name of the table the trigger is on
	ActionStatement   string // SQL statement executed by the trigger
	ActionOrientation string // Orientation of the trigger (ROW or STATEMENT)
	ActionTiming      string // Timing of the trigger (BEFORE or AFTER)
}
