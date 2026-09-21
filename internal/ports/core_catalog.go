package ports

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// CoreCatalog serves the reviewed catalog of proxy-core releases.
//
// IT IS PUBLISHED DATA RATHER THAN A RELEASE LISTING. Which cores may be
// installed — the tier, the evidence matrix behind it, the REALITY compatibility
// table, the per-asset digests, the bilingual summary — is the output of
// acceptance testing. No API derives it, and it cannot be inferred from what a
// release uploaded. The Node project publishes it as a signed document and this
// port reads it, so the panel's selector and its enforcement boundary see the
// same review the node runtime does.
//
// FAILURE IS REFUSE-RATHER-THAN-DEGRADE, WITH ONE EXCEPTION the implementation
// owns: a catalog that cannot be read now but was read a moment ago is served
// from the last good copy, because a transient failure at the origin is not
// evidence that a core release stopped being reviewed.
type CoreCatalog interface {
	// Document returns the whole reviewed catalog.
	Document(ctx context.Context) (CoreCatalogDocument, error)
}

// CoreCatalogReporter is the optional half: a catalog that can say WHAT it is
// serving and how it got it.
//
// IT IS A SEPARATE INTERFACE RATHER THAN A THIRD METHOD on CoreCatalog, and it is
// the same shape the panel adapters use for their optional capabilities. A reader
// that cannot answer — a test double, a build with no source — is still a reader,
// and forcing every implementation to carry a method nobody reads would trade one
// awkwardness for a wider one.
type CoreCatalogReporter interface {
	Status() CoreCatalogStatus
}

// CoreCatalogStatus is what an operator needs to judge whether the review in force
// is the one that is current.
//
// IT SAYS STALENESS AND NOTHING MORE. There is deliberately no field claiming the
// review is current, because this panel cannot know that: it knows when it last
// read a document and what came back. A panel that is offline keeps the review it
// has — that is a decision, not a failure — and while it is offline it does not
// learn about withdrawals. Nothing here should be read as promising otherwise.
type CoreCatalogStatus struct {
	// Source names where the document in force came from, in words an operator can
	// act on.
	Source string `json:"source"`
	// ReviewTime is the document's own statement of when the review it records was
	// made. This is the number that says how old the answer is.
	ReviewTime *time.Time `json:"review_time,omitempty"`
	// LastSuccess is when this process last read and verified a document from the
	// origin. Nil means it never has.
	LastSuccess *time.Time `json:"last_success,omitempty"`
	// LastError is why the most recent attempt did not produce one, if it did not.
	LastError string `json:"last_error,omitempty"`
	// FallingBack is true when the document in force was NOT read from the origin
	// on this attempt.
	FallingBack bool `json:"falling_back"`
}

// CoreCatalogDocument is the published document, and ITS JSON SHAPE IS THE
// CONTRACT: the admin API serves a release verbatim and the SPA declares the same
// fields in web-react/src/api/servers.ts. A field added here and not there is a
// document the panel refuses at decode time, which is how an unknown field is
// turned into a failure at the publisher rather than at a node install.
//
// UpdatedAt is carried and not read. It is the document's own statement of when
// the review it records was made, and a decoder that refused unknown fields would
// reject a document this struct did not name.
type CoreCatalogDocument struct {
	SchemaVersion int           `json:"schema_version"`
	UpdatedAt     time.Time     `json:"updated_at"`
	Releases      []CoreRelease `json:"releases"`
}

// CoreRelease is one reviewed core release.
type CoreRelease struct {
	Engine               string             `json:"engine"`
	Version              string             `json:"version"`
	Tier                 string             `json:"tier"`
	Prerelease           bool               `json:"prerelease"`
	Selectable           bool               `json:"selectable"`
	RequiresConfirmation bool               `json:"requires_confirmation"`
	PublishedAt          time.Time          `json:"published_at"`
	SourceURL            string             `json:"source_url"`
	Summary              CoreLocalizedText  `json:"summary"`
	Reality              CoreReality        `json:"reality"`
	Evidence             CoreEvidence       `json:"evidence"`
	Assets               []CoreReleaseAsset `json:"assets"`
}

type CoreLocalizedText struct {
	EN string `json:"en"`
	ZH string `json:"zh_cn"`
}

type CoreReality struct {
	Xray               string `json:"xray"`
	Mihomo             string `json:"mihomo"`
	SingBox            string `json:"sing_box"`
	URIList            string `json:"uri_list"`
	ServerMinClientVer string `json:"server_min_client_ver,omitempty"`
	MihomoFingerprint  string `json:"mihomo_fingerprint,omitempty"`
	MihomoMLKEM        bool   `json:"mihomo_x25519mlkem768,omitempty"`
}

// CoreEvidence records what was actually exercised before a release was given its
// tier. The panel's install gates read it directly: a tier is a claim, and this is
// the claim's basis.
type CoreEvidence struct {
	SourceAudited   bool            `json:"source_audited"`
	ConfigTested    bool            `json:"config_tested"`
	HandshakeTested bool            `json:"handshake_tested"`
	VerifiedAt      *time.Time      `json:"verified_at,omitempty"`
	Handshakes      []CoreHandshake `json:"handshakes,omitempty"`
}

// CoreHandshake is one client/profile observation. An EXPECTED FAILURE is
// first-class evidence: it is what keeps an incompatible client from being
// reclassified as compatible by editing prose.
type CoreHandshake struct {
	Client   string `json:"client"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Profile  string `json:"profile"`
	Result   string `json:"result"`
}

type CoreReleaseAsset struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Archive string `json:"archive"`
	Binary  string `json:"binary"`
}

// List returns the engine's selectable releases.
//
// NOT SELECTABLE IS NOT THE SAME AS ABSENT. A release the document marks
// unselectable was reviewed and deliberately withheld — it stays in the document
// so its evidence is on the record, and it is not offered.
func (d CoreCatalogDocument) List(engine string) []CoreRelease {
	result := make([]CoreRelease, 0, len(d.Releases))
	for _, release := range d.Releases {
		if release.Engine == engine && release.Selectable {
			result = append(result, release)
		}
	}
	return result
}

// Resolve returns one exact release, normalizing the requested version first so
// that a `v`-prefixed input and a stored canonical one name the same release.
func (d CoreCatalogDocument) Resolve(engine, version string) (CoreRelease, error) {
	normalized, err := domain.NormalizeCoreVersion(version)
	if err != nil {
		return CoreRelease{}, err
	}
	for _, release := range d.List(engine) {
		if release.Version == normalized {
			return release, nil
		}
	}
	return CoreRelease{}, errCoreNotInCatalog(engine, normalized)
}

// Recommended returns the engine's recommended release.
//
// THERE IS EXACTLY ONE, enforced by the publisher: a document with two, or with
// none, is refused before it is signed. This returning an error therefore means
// the catalog is unusable rather than that a choice has to be made here.
func (d CoreCatalogDocument) Recommended(engine string) (CoreRelease, error) {
	for _, release := range d.List(engine) {
		if release.Tier == domain.CoreTierRecommended {
			return release, nil
		}
	}
	return CoreRelease{}, errNoRecommended(engine)
}

// The two selection failures are plain errors rather than domain sentinels,
// deliberately: neither is something a caller got wrong in a way it can fix. An
// unlisted version means the review does not cover it, and a missing recommended
// release means the document should never have been signed — so mapping them to a
// client error would tell an operator to change their input when the catalog is
// what is wrong.
func errCoreNotInCatalog(engine, version string) error {
	return fmt.Errorf("%s %s is not in the selectable core catalog", engine, version)
}

func errNoRecommended(engine string) error {
	return fmt.Errorf("%s has no recommended release", engine)
}
