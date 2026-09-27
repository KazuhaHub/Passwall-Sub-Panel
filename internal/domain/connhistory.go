package domain

// ConnectionRecord is one row of connection_history as the admin reads it:
// one account's source through one panel node, merged across every detector
// sample that saw it. The first and last sightings bound when the account
// connected from there; Count is how many samples saw it — at most one per
// account per half poll interval, so it counts judgements, not requests.
//
// It carries an address. connection_history is the one table PSP stores
// addresses in for this detector (the evidence it judges from never holds
// one), so a record is served to admins only, ages out after
// risk.connection_retention_days and disappears from every read the moment
// its account is deleted. Place holds names only, never coordinates
// (TestNoStoredTypeHoldsACoordinate).
type ConnectionRecord struct {
	UserID int64
	// UPN and DisplayName come from users at read time; the table keeps no
	// name, so a renamed account reads as its new name.
	UPN, DisplayName string
	PanelID          int64
	// Node, SourceKey, IP and Exclusion are LiveConnection's: the node guid
	// ("" for a reader without a node layer), the source (an address, or an
	// IPv6 /64), its smallest member at the LAST sighting, and the rule
	// that set it aside before judging then ("" when it was judged).
	Node, SourceKey, IP, Exclusion string
	// Place is where the geo database put the address at the last sighting.
	Place ConnPlace
	// FirstSeenMS and LastSeenMS are PSP's clock (unix ms) at the first and
	// the latest sample; Count is the number of samples.
	FirstSeenMS, LastSeenMS, Count int64
}
