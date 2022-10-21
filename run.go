package build

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/licensecheck"
	"github.com/pkg/errors"
)

func (b *Builder) RunTarget(name string) error {
	if b.GO_VERSION.LessThan(b.Code.MinGoVersion) {
		return errors.Errorf("unsupported go version %v - shold be at least %v", b.GO_VERSION, b.Code.MinGoVersion)
	}

	ts, err := b.Targets.ComputeTargetRunOrder(name)
	if err != nil {
		return err
	}

	printf := func(i int, format string, a ...interface{}) {
		fmt.Printf("[%v %v/%v] %v\n", time.Now().Format("15:04:05"), i, len(ts), fmt.Sprintf(format, a...))
	}

	for i, n := range ts {
		printf(i, "Executing target %v", n)

		t := b.Targets.Get(n)
		err = t.run()

		if err != nil {
			printf(i, "ERROR executing target %v: %v", n, err)
			return err
		}

		fmt.Println()
	}

	return nil
}

func (b *Builder) RunBuild(exec ExecutableInfo, arch string) error {
	parts := strings.Split(arch, "/")
	goos := parts[0]
	goarch := parts[1]

	var cmd []interface{}

	cmd = append(cmd, "cd "+b.Code.BaseDir, "GOOS="+goos, "GOARCH="+goarch)

	if !exec.GCO {
		cmd = append(cmd, "CGO_ENABLED=0")
	}

	cmd = append(cmd, b.GO, "build")

	for _, a := range exec.BuildArgs {
		cmd = append(cmd, a)
	}

	if len(exec.LDFlags) > 0 || len(exec.LDFlagsVars) > 0 {
		ldflags := exec.LDFlags
		for k, v := range exec.LDFlagsVars {
			ldflags = append(ldflags, "-X", fmt.Sprintf(`"%v=%v"`, k, v))
		}

		cmd = append(cmd, "-ldflags", strings.Join(ldflags, " "))
	}

	output, err := b.GetOutputExecutableName(exec, arch)
	if err != nil {
		return err
	}

	cmd = append(cmd, "-o", output, exec.Path)

	return b.Console.RunInline(cmd...)
}

func (b *Builder) RunCleanZip() error {
	buildDir, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build"))
	if err != nil {
		return err
	}

	files, err := os.ReadDir(buildDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".zip") {
			continue
		}

		err = os.Remove(filepath.Join(buildDir, file.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *Builder) RunZip(exec ExecutableInfo, arch string) error {
	if !exec.Publish {
		return nil
	}

	outputExec, err := b.GetOutputExecutableName(exec, arch)
	if err != nil {
		return err
	}

	_, err = os.Stat(outputExec)
	if err != nil {
		return errors.Wrapf(err, "error accessing compiled executable %v", outputExec)
	}

	outputZip, err := b.GetOutputZipName(exec, arch)
	if err != nil {
		return err
	}

	_ = os.Remove(outputZip)

	oz, err := os.Create(outputZip)
	if err != nil {
		return err
	}
	defer oz.Close()

	oe, err := os.Open(outputExec)
	if err != nil {
		return err
	}
	defer oe.Close()

	zw := zip.NewWriter(oz)
	defer zw.Close()

	ze, err := zw.Create(filepath.Base(outputExec))
	if err != nil {
		return err
	}

	_, err = io.Copy(ze, oe)
	if err != nil {
		return err
	}

	return nil
}

func (b *Builder) GetOutputZipName(exec ExecutableInfo, arch string) (string, error) {
	name := fmt.Sprintf("%v-%v-%v.zip", exec.Name, b.Code.Version, strings.ReplaceAll(arch, "/", "_"))
	name = fixFilename(name)

	output, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build", name))
	if err != nil {
		return "", err
	}

	return output, nil
}

func (b *Builder) GetOutputExecutableName(exec ExecutableInfo, arch string) (string, error) {
	name := exec.Name
	if strings.HasPrefix(arch, "windows/") {
		name += ".exe"
	}

	output, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build", arch, name))
	if err != nil {
		return "", err
	}

	return output, nil
}

func (b *Builder) RunLicenseCheck() error {
	modCacheRoot, err := b.loadModCacheRoot()
	if err != nil {
		return err
	}

	deps, err := b.loadDependencies()
	if err != nil {
		return err
	}

	for _, dep := range deps {
		err = b.fillLicenseInfo(dep, modCacheRoot)
		if err != nil {
			return err
		}
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Path < deps[j].Path
	})

	for _, dep := range deps {
		var names []string
		for _, l := range dep.Licenses {
			if l.Name != "" {
				names = append(names, l.Name)
			}
		}

		license := strings.Join(names, ", ")
		if license == "" {
			license = "Unknown"
		}

		fmt.Printf("%v %v : %v\n", dep.Path, dep.Version, license)
	}

	return nil
}

func (b *Builder) loadModCacheRoot() (string, error) {
	root, err := b.Console.RunAndReturnOutput(b.GO, "env", "GOMODCACHE")
	if err != nil {
		return "", err
	}

	root = addSeparatorAtEnd(root)

	return root, nil
}

func (b *Builder) loadDependencies() ([]*modDependency, error) {
	output, err := b.Console.RunAndReturnOutput(b.GO, "mod", "download", "-json")
	if err != nil {
		return nil, err
	}

	var deps []*modDependency

	dec := json.NewDecoder(strings.NewReader(output))
	for {
		var dep modDependency

		err = dec.Decode(&dep)

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, err
		}

		dep.Dir = addSeparatorAtEnd(dep.Dir)

		deps = append(deps, &dep)
	}

	return deps, nil
}

var licenseNameRE = regexp.MustCompile("^(.*)" +
	"(?:-([0-9.]+[a-z]?)|2.0-or-3.0)?" +
	"(?:-(?:no-)?(copyleft-exception|only|or-later|Perl|cl8|AT|IGO|US|Glyph|invariants-or-later|invariants-only|CMU|" +
	"fallback|Clear|Patent|Views|Attribution|LBNL|No-Nuclear-License(?:-2014)?|No-Nuclear-Warranty|NoTrademark|Open-MPI|" +
	"UC|GPL-Compatible|sell-variant|P|R|Rplus|NoAd|advertising|enna|feh))?$")

func (b *Builder) fillLicenseInfo(dep *modDependency, modCacheRoot string) error {
	licenseFileNames, err := b.findLicenseFilesSearchingParents(dep, modCacheRoot)
	if err != nil {
		return err
	}

	for _, licenseFileName := range licenseFileNames {
		data, err := os.ReadFile(licenseFileName)
		if err != nil {
			return err
		}

		license := licenseInfo{
			Contents: string(data),
		}

		cov := licensecheck.Scan(data)
		if cov.Percent >= 75 { // Same as pkg.go.dev
			license.Name = cov.Match[0].ID

			matches := licenseNameRE.FindStringSubmatch(license.Name)
			if len(matches) > 1 {
				license.Type = matches[1]
				license.Version = matches[2]
				license.Modifier = matches[3]
			}
		}

		dep.Licenses = append(dep.Licenses, license)
	}

	return nil
}

func (b *Builder) findLicenseFilesSearchingParents(dep *modDependency, modCacheRoot string) ([]string, error) {
	path := dep.Dir
	for len(path) > len(modCacheRoot) {
		fileNames, err := b.findLicenseFiles(path)
		if err != nil {
			return nil, err
		}

		if len(fileNames) > 0 {
			return fileNames, nil
		}

		// Try parent folder
		path = addSeparatorAtEnd(filepath.Dir(path))
	}

	return nil, nil
}

var licenseFiles = map[string]bool{
	"copying":            true,
	"licence":            true,
	"license":            true,
	"licence-2.0":        true,
	"license-2.0":        true,
	"licence-apache":     true,
	"license-apache":     true,
	"licence-apache-2.0": true,
	"license-apache-2.0": true,
	"licence-mit":        true,
	"license-mit":        true,
	"licenceInfo":        true,
	"licenseInfo":        true,
	"licenceInfo-2.0":    true,
	"licenseInfo-2.0":    true,
	"licenceInfo-apache": true,
	"licenseInfo-apache": true,
	"licenceInfo-mit":    true,
	"licenseInfo-mit":    true,
	"mit-licence":        true,
	"mit-license":        true,
	"mit-licenceInfo":    true,
	"mit-licenseInfo":    true,
}

var licenseExtensions = map[string]bool{
	"":          true,
	".code":     true,
	".docs":     true,
	".markdown": true,
	".md":       true,
	".mit":      true,
	".rst":      true,
	".txt":      true,
}

func (b *Builder) findLicenseFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var result []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := strings.ToLower(entry.Name())
		ext := filepath.Ext(name)
		if !licenseFiles[name[:len(name)-len(ext)]] || !licenseExtensions[ext] {
			continue
		}

		result = append(result, filepath.Join(path, entry.Name()))
	}

	return result, nil
}

type modDependency struct {
	Path     string
	Version  string
	Dir      string
	Licenses []licenseInfo
}

type licenseInfo struct {
	Name     string
	Type     string
	Contents string
	Modifier string
	Version  string
}

func addSeparatorAtEnd(dir string) string {
	if dir == "" {
		return dir
	}

	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}

	return dir
}

const (
	LICENSE_0BSD                    = "0BSD"
	LICENSE_AAL                     = "AAL"
	LICENSE_ADSL                    = "ADSL"
	LICENSE_AFL                     = "AFL"
	LICENSE_AGPL                    = "AGPL"
	LICENSE_AMDPLPA                 = "AMDPLPA"
	LICENSE_AML                     = "AML"
	LICENSE_AMPAS                   = "AMPAS"
	LICENSE_ANTLR_PD                = "ANTLR-PD"
	LICENSE_APAFML                  = "APAFML"
	LICENSE_APL                     = "APL"
	LICENSE_ABSTYLES                = "Abstyles"
	LICENSE_ADOBE                   = "Adobe"
	LICENSE_AFMPARSE                = "Afmparse"
	LICENSE_ALADDIN                 = "Aladdin"
	LICENSE_ANTI996                 = "Anti996"
	LICENSE_APACHE                  = "Apache"
	LICENSE_ARTISTIC                = "Artistic"
	LICENSE_BSD_1_CLAUSE            = "BSD-1-Clause"
	LICENSE_BSD_2_CLAUSE            = "BSD-2-Clause"
	LICENSE_BSD_3_CLAUSE            = "BSD-3-Clause"
	LICENSE_BSD_4_CLAUSE            = "BSD-4-Clause"
	LICENSE_BSD_PROTECTION          = "BSD-Protection"
	LICENSE_BSD_SOURCE_CODE         = "BSD-Source-Code"
	LICENSE_BSL                     = "BSL"
	LICENSE_BAHYPH                  = "Bahyph"
	LICENSE_BARR                    = "Barr"
	LICENSE_BEERWARE                = "Beerware"
	LICENSE_BITTORRENT              = "BitTorrent"
	LICENSE_BLUEOAK                 = "BlueOak"
	LICENSE_BORCEUX                 = "Borceux"
	LICENSE_CAL                     = "CAL"
	LICENSE_CATOSL                  = "CATOSL"
	LICENSE_CC_BY                   = "CC-BY"
	LICENSE_CC_BY_NC                = "CC-BY-NC"
	LICENSE_CC_BY_NC_ND             = "CC-BY-NC-ND"
	LICENSE_CC_BY_NC_SA             = "CC-BY-NC-SA"
	LICENSE_CC_BY_ND                = "CC-BY-ND"
	LICENSE_CC_BY_SA                = "CC-BY-SA"
	LICENSE_CC_PDDC                 = "CC-PDDC"
	LICENSE_CC0                     = "CC0"
	LICENSE_CDDL                    = "CDDL"
	LICENSE_CDLA_PERMISSIVE         = "CDLA-Permissive"
	LICENSE_CDLA_SHARING            = "CDLA-Sharing"
	LICENSE_CECILL                  = "CECILL"
	LICENSE_CECILL_B                = "CECILL-B"
	LICENSE_CECILL_C                = "CECILL-C"
	LICENSE_CERN_OHL                = "CERN-OHL"
	LICENSE_CERN_OHL_P              = "CERN-OHL-P"
	LICENSE_CNRI_JYTHON             = "CNRI-Jython"
	LICENSE_CNRI_PYTHON             = "CNRI-Python"
	LICENSE_CPAL                    = "CPAL"
	LICENSE_CPL                     = "CPL"
	LICENSE_CPOL                    = "CPOL"
	LICENSE_CUA_OPL                 = "CUA-OPL"
	LICENSE_CALDERA                 = "Caldera"
	LICENSE_CLARTISTIC              = "ClArtistic"
	LICENSE_COMMONSCLAUSE           = "CommonsClause"
	LICENSE_CONDOR                  = "Condor"
	LICENSE_CROSSWORD               = "Crossword"
	LICENSE_CRYSTALSTACKER          = "CrystalStacker"
	LICENSE_CUBE                    = "Cube"
	LICENSE_D_FSL                   = "D-FSL"
	LICENSE_DOC                     = "DOC"
	LICENSE_DSDP                    = "DSDP"
	LICENSE_DOTSEQN                 = "Dotseqn"
	LICENSE_ECL                     = "ECL"
	LICENSE_EFL                     = "EFL"
	LICENSE_EPICS                   = "EPICS"
	LICENSE_EPL                     = "EPL"
	LICENSE_EUDATAGRID              = "EUDatagrid"
	LICENSE_EUPL                    = "EUPL"
	LICENSE_ENTESSA                 = "Entessa"
	LICENSE_ERLPL                   = "ErlPL"
	LICENSE_EUROSYM                 = "Eurosym"
	LICENSE_FSFAP                   = "FSFAP"
	LICENSE_FSFUL                   = "FSFUL"
	LICENSE_FSFULLR                 = "FSFULLR"
	LICENSE_FTL                     = "FTL"
	LICENSE_FAIR                    = "Fair"
	LICENSE_FRAMEWORX               = "Frameworx"
	LICENSE_FREEIMAGE               = "FreeImage"
	LICENSE_GFDL                    = "GFDL"
	LICENSE_GL2PS                   = "GL2PS"
	LICENSE_GLWTPL                  = "GLWTPL"
	LICENSE_GPL                     = "GPL"
	LICENSE_GIFTWARE                = "Giftware"
	LICENSE_GLIDE                   = "Glide"
	LICENSE_GLULXE                  = "Glulxe"
	LICENSE_GOOGLEPATENTCLAUSE      = "GooglePatentClause"
	LICENSE_GOOGLEPATENTSFILE       = "GooglePatentsFile"
	LICENSE_HPND                    = "HPND"
	LICENSE_HASKELLREPORT           = "HaskellReport"
	LICENSE_HIPPOCRATIC             = "Hippocratic"
	LICENSE_IBM_PIBS                = "IBM-pibs"
	LICENSE_ICU                     = "ICU"
	LICENSE_IJG                     = "IJG"
	LICENSE_IPA                     = "IPA"
	LICENSE_IPL                     = "IPL"
	LICENSE_ISC                     = "ISC"
	LICENSE_IMAGEMAGICK             = "ImageMagick"
	LICENSE_IMLIB2                  = "Imlib2"
	LICENSE_INFO_ZIP                = "Info-ZIP"
	LICENSE_INTEL                   = "Intel"
	LICENSE_INTEL_ACPI              = "Intel-ACPI"
	LICENSE_INTERBASE               = "Interbase"
	LICENSE_JPNIC                   = "JPNIC"
	LICENSE_JSON                    = "JSON"
	LICENSE_JASPER                  = "JasPer"
	LICENSE_LAL                     = "LAL"
	LICENSE_LGPL                    = "LGPL"
	LICENSE_LGPLLR                  = "LGPLLR"
	LICENSE_LPL                     = "LPL"
	LICENSE_LPPL                    = "LPPL"
	LICENSE_LATEX2E                 = "Latex2e"
	LICENSE_LEPTONICA               = "Leptonica"
	LICENSE_LILIQ                   = "LiLiQ"
	LICENSE_LIBPNG                  = "Libpng"
	LICENSE_LINUX_OPENIB            = "Linux-OpenIB"
	LICENSE_MIT                     = "MIT"
	LICENSE_MITNFA                  = "MITNFA"
	LICENSE_MPL                     = "MPL"
	LICENSE_MS_PL                   = "MS-PL"
	LICENSE_MS_RL                   = "MS-RL"
	LICENSE_MTLL                    = "MTLL"
	LICENSE_MAKEINDEX               = "MakeIndex"
	LICENSE_MIROS                   = "MirOS"
	LICENSE_MOTOSOTO                = "Motosoto"
	LICENSE_MULANPSL                = "MulanPSL"
	LICENSE_MULTICS                 = "Multics"
	LICENSE_MUP                     = "Mup"
	LICENSE_NASA                    = "NASA"
	LICENSE_NBPL                    = "NBPL"
	LICENSE_NCGL_UK                 = "NCGL-UK"
	LICENSE_NCSA                    = "NCSA"
	LICENSE_NGPL                    = "NGPL"
	LICENSE_NIST_PD                 = "NIST-PD"
	LICENSE_NLOD                    = "NLOD"
	LICENSE_NLPL                    = "NLPL"
	LICENSE_NOSL                    = "NOSL"
	LICENSE_NPL                     = "NPL"
	LICENSE_NPOSL                   = "NPOSL"
	LICENSE_NRL                     = "NRL"
	LICENSE_NTP                     = "NTP"
	LICENSE_NAUMEN                  = "Naumen"
	LICENSE_NET_SNMP                = "Net-SNMP"
	LICENSE_NETCDF                  = "NetCDF"
	LICENSE_NEWSLETR                = "Newsletr"
	LICENSE_NOKIA                   = "Nokia"
	LICENSE_NOWEB                   = "Noweb"
	LICENSE_O_UDA                   = "O-UDA"
	LICENSE_OCCT_PL                 = "OCCT-PL"
	LICENSE_OCLC                    = "OCLC"
	LICENSE_ODC_BY                  = "ODC-By"
	LICENSE_ODBL                    = "ODbL"
	LICENSE_OFL                     = "OFL"
	LICENSE_OGC                     = "OGC"
	LICENSE_OGL_CANADA              = "OGL-Canada"
	LICENSE_OGL_UK                  = "OGL-UK"
	LICENSE_OGTSL                   = "OGTSL"
	LICENSE_OLDAP                   = "OLDAP"
	LICENSE_OML                     = "OML"
	LICENSE_OPL                     = "OPL"
	LICENSE_OSET_PL                 = "OSET-PL"
	LICENSE_OSL                     = "OSL"
	LICENSE_OPENSSL                 = "OpenSSL"
	LICENSE_PDDL                    = "PDDL"
	LICENSE_PHP                     = "PHP"
	LICENSE_PSF                     = "PSF"
	LICENSE_PARITY                  = "Parity"
	LICENSE_PLEXUS                  = "Plexus"
	LICENSE_POLYFORM_NONCOMMERCIAL  = "PolyForm-Noncommercial"
	LICENSE_POLYFORM_SMALL_BUSINESS = "PolyForm-Small-Business"
	LICENSE_POSTGRESQL              = "PostgreSQL"
	LICENSE_PROSPERITY              = "Prosperity"
	LICENSE_PYTHON                  = "Python"
	LICENSE_QPL                     = "QPL"
	LICENSE_QHULL                   = "Qhull"
	LICENSE_RHECOS                  = "RHeCos"
	LICENSE_RPL                     = "RPL"
	LICENSE_RPSL                    = "RPSL"
	LICENSE_RSA_MD                  = "RSA-MD"
	LICENSE_RSCPL                   = "RSCPL"
	LICENSE_RDISC                   = "Rdisc"
	LICENSE_RUBY                    = "Ruby"
	LICENSE_SAX_PD                  = "SAX-PD"
	LICENSE_SCEA                    = "SCEA"
	LICENSE_SGI_B                   = "SGI-B"
	LICENSE_SHL                     = "SHL"
	LICENSE_SISSL                   = "SISSL"
	LICENSE_SMLNJ                   = "SMLNJ"
	LICENSE_SMPPL                   = "SMPPL"
	LICENSE_SNIA                    = "SNIA"
	LICENSE_SPL                     = "SPL"
	LICENSE_SSH_OPENSSH             = "SSH-OpenSSH"
	LICENSE_SSH_SHORT               = "SSH-short"
	LICENSE_SSPL                    = "SSPL"
	LICENSE_SWL                     = "SWL"
	LICENSE_SAXPATH                 = "Saxpath"
	LICENSE_SENDMAIL                = "Sendmail"
	LICENSE_SIMPL                   = "SimPL"
	LICENSE_SLEEPYCAT               = "Sleepycat"
	LICENSE_SPENCER                 = "Spencer"
	LICENSE_SUGARCRM                = "SugarCRM"
	LICENSE_TAPR_OHL                = "TAPR-OHL"
	LICENSE_TCL                     = "TCL"
	LICENSE_TCP_WRAPPERS            = "TCP-wrappers"
	LICENSE_TMATE                   = "TMate"
	LICENSE_TORQUE                  = "TORQUE"
	LICENSE_TOSL                    = "TOSL"
	LICENSE_TU_BERLIN               = "TU-Berlin"
	LICENSE_UCL                     = "UCL"
	LICENSE_UPL                     = "UPL"
	LICENSE_UNICODE_DFS             = "Unicode-DFS"
	LICENSE_UNICODE_TOU             = "Unicode-TOU"
	LICENSE_UNLICENSE               = "Unlicense"
	LICENSE_VOSTROM                 = "VOSTROM"
	LICENSE_VSL                     = "VSL"
	LICENSE_VIM                     = "Vim"
	LICENSE_W3C                     = "W3C"
	LICENSE_WTFPL                   = "WTFPL"
	LICENSE_WATCOM                  = "Watcom"
	LICENSE_WSUIPA                  = "Wsuipa"
	LICENSE_X11                     = "X11"
	LICENSE_XFREE86                 = "XFree86"
	LICENSE_XSKAT                   = "XSkat"
	LICENSE_XEROX                   = "Xerox"
	LICENSE_XNET                    = "Xnet"
	LICENSE_YPL                     = "YPL"
	LICENSE_ZPL                     = "ZPL"
	LICENSE_ZED                     = "Zed"
	LICENSE_ZEND                    = "Zend"
	LICENSE_ZIMBRA                  = "Zimbra"
	LICENSE_ZLIB                    = "Zlib"
	LICENSE_BLESSING                = "blessing"
	LICENSE_BZIP2                   = "bzip2"
	LICENSE_COPYLEFT_NEXT           = "copyleft-next"
	LICENSE_CURL                    = "curl"
	LICENSE_DIFFMARK                = "diffmark"
	LICENSE_DVIPDFM                 = "dvipdfm"
	LICENSE_EGENIX                  = "eGenix"
	LICENSE_ETALAB                  = "etalab"
	LICENSE_GSOAP                   = "gSOAP"
	LICENSE_GNUPLOT                 = "gnuplot"
	LICENSE_IMATIX                  = "iMatix"
	LICENSE_LIBSELINUX              = "libselinux"
	LICENSE_LIBTIFF                 = "libtiff"
	LICENSE_MPICH2                  = "mpich2"
	LICENSE_PSFRAG                  = "psfrag"
	LICENSE_PSUTILS                 = "psutils"
	LICENSE_XINETD                  = "xinetd"
	LICENSE_XPP                     = "xpp"
	LICENSE_ZLIB_ACKNOWLEDGEMENT    = "zlib-acknowledgement"
)
