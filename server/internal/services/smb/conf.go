package smb

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Sakana-1314/MList/server/internal/store"
)

// confParams are the inputs of the generated smb.conf besides the users.
type confParams struct {
	Dir        string // samba state directory (<data_dir>/samba)
	Port       int
	Workgroup  string
	UserPrefix string
	ForceUser  string
}

// confResult is the outcome of buildConfig.
type confResult struct {
	Conf string
	// Shares maps user id -> mount name -> share name for every included mount.
	Shares map[uint]map[string]string
	// Excluded lists accounts that cannot be used with Samba (invalid name or
	// several users mapping to the same account) and must not be synced.
	Excluded map[string]string // account -> reason
	// Skipped lists mounts left out of the configuration, with the reason.
	Skipped []string
}

// unsafeShareChars are replaced in share names: control characters (which
// would allow injecting configuration lines), characters Windows rejects in
// share names, '%' (Samba variable substitution) and list separators.
var unsafeShareChars = regexp.MustCompile(`[\[\]\x00-\x1f\x7f\\/:*?"<>|;=,+%]`)

// validAccount matches names usable as Linux system and Samba accounts.
var validAccount = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)

// reservedShares are section names with special meaning in smb.conf.
var reservedShares = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true}

const maxShareName = 80 // Windows Explorer cannot open longer share names

func accountName(prefix, username string) string { return strings.ToLower(prefix + username) }

// shareName turns a mount name into a safe share name.
func shareName(name string) string {
	n := strings.TrimSpace(unsafeShareChars.ReplaceAllString(name, "_"))
	if utf8.RuneCountInString(n) > maxShareName {
		n = string([]rune(n)[:maxShareName])
	}
	if n == "" {
		n = "share"
	}
	if reservedShares[strings.ToLower(n)] {
		n += "_"
	}
	return n
}

// cleanValue strips characters that would break out of a single smb.conf value.
func cleanValue(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
}

// unsafePath reports why a directory cannot be written into smb.conf, or "".
// smb.conf has no quoting/escaping: line breaks would inject directives, '%'
// triggers variable substitution and surrounding blanks are trimmed by Samba.
func unsafePath(p string) string {
	switch {
	case !filepath.IsAbs(p):
		return "不是绝对路径"
	case strings.ContainsAny(p, "\x00\r\n"), strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		return "包含控制字符"
	case strings.Contains(p, "%"):
		return "包含 % 字符 (Samba 变量替换)"
	case strings.TrimSpace(p) != p:
		return "首尾包含空白字符"
	}
	return ""
}

type share struct {
	name  string
	path  string
	read  []string
	write []string
}

// buildConfig generates smb.conf: one share per distinct (name, directory)
// among the enabled users' mounts. Users with a writable mount go to the
// share's write list, everyone else gets read access. A mount name used for
// different directories gets the username appended; remaining clashes are
// resolved with a numeric suffix (share names are case-insensitive).
func buildConfig(users []store.User, p confParams) confResult {
	res := confResult{Shares: map[uint]map[string]string{}, Excluded: map[string]string{}}

	users = append([]store.User(nil), users...)
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })

	// Accounts: drop invalid names and accounts shared by several users
	// (usernames are case-sensitive, accounts are lower-cased).
	owners := map[string]int{}
	for _, u := range users {
		if !u.Disabled {
			owners[accountName(p.UserPrefix, u.Username)]++
		}
	}
	var active []store.User
	for _, u := range users {
		if u.Disabled {
			continue
		}
		acct := accountName(p.UserPrefix, u.Username)
		switch {
		case !validAccount.MatchString(acct):
			res.Excluded[acct] = "用户名不能用作系统账户名"
		case acct == "root":
			res.Excluded[acct] = "不允许映射到 root 账户，请设置 smb.user_prefix"
		case owners[acct] > 1:
			res.Excluded[acct] = "多个用户映射到同一个 SMB 账户（用户名仅大小写不同）"
		default:
			active = append(active, u)
		}
	}

	// Mount names that refer to more than one directory.
	pathsByName := map[string]map[string]bool{}
	for _, u := range active {
		for _, mt := range u.Mounts {
			k := strings.ToLower(shareName(mt.Name))
			if pathsByName[k] == nil {
				pathsByName[k] = map[string]bool{}
			}
			active, _ := mt.Active()
			pathsByName[k][filepath.Clean(active)] = true
		}
	}

	byName := map[string]*share{} // lower-cased share name -> share
	var list []*share
	for _, u := range active {
		acct := accountName(p.UserPrefix, u.Username)
		for _, mt := range u.Mounts {
			// SMB serves the bucket's active directory; changes made there reach
			// the backup directories through the periodic bucket sync.
			active, _ := mt.Active()
			if why := unsafePath(active); why != "" {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s/%s: %s", u.Username, mt.Name, why))
				continue
			}
			path := filepath.Clean(active)
			base := shareName(mt.Name)
			if len(pathsByName[strings.ToLower(base)]) > 1 {
				base = shareName(base + "-" + u.Username)
			}
			name := base
			sh := byName[strings.ToLower(name)]
			for i := 2; sh != nil && sh.path != path; i++ {
				name = fmt.Sprintf("%s-%d", base, i)
				sh = byName[strings.ToLower(name)]
			}
			if sh == nil {
				sh = &share{name: name, path: path}
				byName[strings.ToLower(name)] = sh
				list = append(list, sh)
			}
			if mt.Writable {
				sh.write = appendUnique(sh.write, acct)
			} else {
				sh.read = appendUnique(sh.read, acct)
			}
			if res.Shares[u.ID] == nil {
				res.Shares[u.ID] = map[string]string{}
			}
			res.Shares[u.ID][mt.Name] = sh.name
		}
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].name) < strings.ToLower(list[j].name) })

	workgroup := cleanValue(p.Workgroup)
	if workgroup == "" {
		workgroup = "WORKGROUP"
	}
	dir := func(sub string) string { return filepath.Join(p.Dir, sub) }
	var b strings.Builder
	fmt.Fprintf(&b, `# Generated by MList. Do not edit; changes are overwritten.
[global]
  workgroup = %s
  server string = MList
  netbios name = LNFILES
  smb ports = %d
  security = user
  map to guest = never
  restrict anonymous = 2
  server min protocol = SMB2_10
  passdb backend = tdbsam:%s
  private dir = %s
  lock directory = %s
  state directory = %s
  cache directory = %s
  pid directory = %s
  ncalrpc dir = %s
  log level = 1
  load printers = no
  printing = bsd
  printcap name = /dev/null
  disable spoolss = yes
  disable netbios = yes
  unix extensions = no
  wide links = no
  follow symlinks = yes
  vfs objects = catia fruit streams_xattr
  fruit:metadata = stream
  fruit:posix_rename = yes
  veto files = /.lnf-upload-*/
`, workgroup, p.Port, filepath.Join(dir("private"), "passdb.tdb"),
		dir("private"), dir("lock"), dir("state"), dir("cache"), dir("run"), dir("ncalrpc"))

	forceUser := cleanValue(p.ForceUser)
	for _, sh := range list {
		all := append(append([]string{}, sh.read...), sh.write...)
		// An empty "valid users" would admit everyone; such shares cannot exist
		// here because a share is only created together with its first user.
		fmt.Fprintf(&b, "\n[%s]\n  path = %s\n  browseable = yes\n  read only = yes\n  valid users = %s\n",
			sh.name, sh.path, strings.Join(all, " "))
		if len(sh.write) > 0 {
			fmt.Fprintf(&b, "  write list = %s\n", strings.Join(sh.write, " "))
		}
		if forceUser != "" {
			fmt.Fprintf(&b, "  force user = %s\n", forceUser)
		}
		b.WriteString("  create mask = 0644\n  directory mask = 0755\n")
	}
	res.Conf = b.String()
	return res
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
