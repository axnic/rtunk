package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ConfigSuite's fixtures are real config documents loaded once from
// fixtures/ (SPECS.md-style examples and an actual `trunk init` output),
// rather than embedded as Go string literals per test.
type ConfigSuite struct {
	suite.Suite

	fullExample     []byte
	realTrunkYAML   []byte
	govetDefinition []byte
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}

func (s *ConfigSuite) SetupSuite() {
	s.fullExample = s.readFixture("full_example.yaml")
	s.realTrunkYAML = s.readFixture("real_trunk.yaml")
	s.govetDefinition = s.readFixture("govet_definition.yaml")
}

func (s *ConfigSuite) readFixture(name string) []byte {
	data, err := os.ReadFile(filepath.Join("fixtures", name))
	s.Require().NoError(err, "reading fixture %s", name)
	return data
}

func (s *ConfigSuite) TestLoad_PrefersRtunkOverTrunk() {
	dir := s.T().TempDir()
	writeFile(s.T(), filepath.Join(dir, ".rtunk/rtunk.yaml"), "version: 0.1\nrepo:\n  trunk_branch: develop\ncache:\n  dir: /tmp/rtunk-cache\n")
	writeFile(s.T(), filepath.Join(dir, ".trunk/trunk.yaml"), "version: 0.1\nrepo:\n  trunk_branch: should-not-be-used\n")

	cfg, err := Load(dir)
	s.Require().NoError(err)
	s.Equal("develop", cfg.Repo.TrunkBranch)
	s.Equal("/tmp/rtunk-cache", cfg.Cache.Dir)
}

func (s *ConfigSuite) TestLoad_FallsBackToTrunkYaml() {
	dir := s.T().TempDir()
	writeFile(s.T(), filepath.Join(dir, ".trunk/trunk.yaml"), "version: 0.1\nrepo:\n  trunk_branch: develop\n")

	cfg, err := Load(dir)
	s.Require().NoError(err)
	s.Equal("develop", cfg.Repo.TrunkBranch)
}

func (s *ConfigSuite) TestLoad_DefaultsWhenNoConfig() {
	cfg, err := Load(s.T().TempDir())
	s.Require().NoError(err)
	s.Equal("main", cfg.Repo.TrunkBranch)
}

func (s *ConfigSuite) TestParse_RejectsUnsupportedVersion() {
	_, err := Parse([]byte("version: 0.2\nrepo:\n  trunk_branch: main\n"))
	s.Error(err)
}

func (s *ConfigSuite) TestParse_RejectsMissingVersion() {
	_, err := Parse([]byte("repo:\n  trunk_branch: main\n"))
	s.Error(err)
}

func (s *ConfigSuite) TestParse_RejectsWrongFieldType() {
	// lint.enabled must be an array of strings, not a single string.
	_, err := Parse([]byte("version: 0.1\nlint:\n  enabled: not-a-list\n"))
	s.Error(err)
}

func (s *ConfigSuite) TestParse_FullExample() {
	c, err := Parse(s.fullExample)
	s.Require().NoError(err)

	s.Equal(0.1, c.Version)
	s.Equal("1.0.0", c.CLI.Version)
	s.Require().Len(c.CLI.Options, 2)
	s.Equal("-y", c.CLI.Options[1].Args[0])
	s.Equal("main", c.Repo.TrunkBranch)
	s.False(c.Repo.UseBranchUpstream)

	s.Require().Len(c.Runtimes.Enabled, 2)
	s.Equal(PackageVersion("node@20.11.0"), c.Runtimes.Enabled[0])

	s.Require().Len(c.Plugins.Sources, 1)
	s.Equal("rtunk-core", c.Plugins.Sources[0].ID)
	s.Equal("v1.0.0", c.Plugins.Sources[0].Ref)

	s.Require().Len(c.Lint.Definitions, 1)
	def := c.Lint.Definitions[0]
	s.Equal("my-internal-linter", def.Name)
	s.Equal(GlobPattern("ALL"), def.Files[0])
	s.Require().Len(def.Commands, 1)
	s.Equal("regex", def.Commands[0].Output)
	s.Equal(1, def.Commands[0].SuccessCodes[1])

	s.Require().Len(c.Lint.Enabled, 2)
	s.Require().Len(c.Lint.Disabled, 1)
	s.Equal(PackageVersion("rufo"), c.Lint.Disabled[0])
	s.Require().Len(c.Lint.Ignore, 2)
	s.Equal(GlobPattern("!**/generated/**/*.keep"), c.Lint.Ignore[0].Paths[1])
	s.Require().Len(c.Lint.Triggers, 1)
	s.Equal("ansible-lint", c.Lint.Triggers[0].Linters[0])

	s.Require().Len(c.Tools.Enabled, 1)
	s.Require().Len(c.Tools.Definitions, 1)
	s.Equal("2.45.0", c.Tools.Definitions[0].KnownGoodVersion)
	s.Len(c.Actions.Enabled, 2)
	s.Len(c.Actions.Disabled, 1)
	s.False(c.Telemetry)
}

// realTrunkYAML is a real `.trunk/trunk.yaml` produced by `trunk init`, to
// prove rtunk.yaml/trunk.yaml compatibility on an actual third-party file
// rather than just our own annotated example.
func (s *ConfigSuite) TestParse_RealTrunkYAML() {
	c, err := Parse(s.realTrunkYAML)
	s.Require().NoError(err)

	s.Equal(0.1, c.Version)
	s.Equal("1.25.0", c.CLI.Version)
	s.Require().Len(c.Plugins.Sources, 1)
	s.Equal("https://github.com/trunk-io/plugins", c.Plugins.Sources[0].URI)
	s.Len(c.Runtimes.Enabled, 3)
	s.Require().Len(c.Lint.Enabled, 10)
	s.Equal(PackageVersion("gofmt@1.20.4"), c.Lint.Enabled[2])
	s.Len(c.Actions.Enabled, 4)
	// rtunk-only extension, absent from a real trunk.yaml: must not error, zero value.
	s.Empty(c.Cache.Dir)
}

func (s *ConfigSuite) TestParseLinterDefinition() {
	d, err := ParseLinterDefinition(s.govetDefinition)
	s.Require().NoError(err)

	s.Equal("govet", d.Name)
	s.Require().Len(d.Commands, 1)
	s.Equal("warning", d.Commands[0].Severity)
	s.True(d.Matches("internal/exec/exec.go"))
	s.False(d.Matches("README.md"))
}
