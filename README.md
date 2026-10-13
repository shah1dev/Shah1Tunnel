# Shah1Tunnel
Shah1Tunnel — Secure Tunnel Setup, Configuration and Management

---

<!-- Language switch -->
[English](#english) | [فارسی](#فارسی)

## Easy install / نصب آسان

Copy & paste the commands below to clone this repository and run the installer locally. These commands assume a Unix-like system with git, bash, tar and coreutils installed.

Bash (recommended):

```bash
# clone repository
git clone https://github.com/shah1dev/Shah1Tunnel.git
cd Shah1Tunnel

# If the installer archive filename is missing, create a symlink (see note below)
if [ -f "S1T-CAPSULE/S1T-SOURCE.tar.gz" ]; then
  sudo bash S1T-START.sh
elif [ -f "S1T-CAPSULE/null" ]; then
  # the repository contains a file named 'null' inside S1T-CAPSULE; create a safe symlink
  ln -sf "$(pwd)/S1T-CAPSULE/null" "$(pwd)/S1T-CAPSULE/S1T-SOURCE.tar.gz"
  sudo bash S1T-START.sh
else
  echo "ERROR: required package archive missing: S1T-CAPSULE/S1T-SOURCE.tar.gz or S1T-CAPSULE/null" >&2
  exit 1
fi
```

Direct raw file links (for manual inspection):
- S1T-START.sh (installer script): https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/main/S1T-START.sh
- S1T-CHECKS.sha256 (checksums): https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/main/S1T-CHECKS.sha256
- S1T-CAPSULE content (binary file in repo): https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/main/S1T-CAPSULE/null

NOTE: I cannot run these commands from this environment. The installer is designed to be executed on your machine or server. The README below documents exact behavior, checks and known issues so you can run it safely.

---

# English

## What this is
Shah1Tunnel is a branded manager and installer that restores an embedded source capsule (WatTunnel engine v2.4.12) and runs the official binary-based installation. It provides a small shell-based installer (S1T-START.sh) which verifies checksums, allocates a workspace, unpacks a capsule and runs the bundled setup scripts.

### Stack
- Language(s): shell scripts (Bash); the packaged source indicates a Go project (go.mod, cmd/*/*.go) inside the embedded archive
- Runtime: bash + coreutils + tar + base64
- Notable components: S1T-START.sh (installer), S1T-CHECKS.sha256 (integrity manifest), S1T-CAPSULE/* (embedded source/archive)

## Top-level layout
```
README.md                - this file
S1T-START.sh             - main installer/launcher script (bash)
S1T-CHECKS.sha256        - expected sha256 checksums for package files
S1T-INFO-FA.txt          - Farsi information / release notes
S1T-CAPSULE/             - embedded package directory (contains a binary file in this repo)
```

## Prerequisites
- A Unix-like host with bash, tar, sha256sum, base64 and coreutils available.
- At least 16 MiB free on one of: $WAT_TMPDIR, /var/tmp, /root/.cache/shah1tun, or /tmp (the script checks these paths). The script refuses to run without that free space.

## Installation (short)
1. git clone ...
2. cd Shah1Tunnel
3. Ensure S1T-CHECKS.sha256 and the capsule file exist in the repository root.
4. Run: sudo bash S1T-START.sh

## How the installer works (important details)
- The installer performs an integrity check by running: sha256sum --status -c S1T-CHECKS.sha256
  If this fails, the installer stops without modifying files.
- It then allocates a disk-backed workspace (not a small tmpfs). It checks multiple locations and requires at least 16 MiB free.
- The embedded capsule is unpacked in the workspace; filenames inside the capsule are encoded with the S1T_ prefix and are decoded by the installer.
- After unpack, the script restores a package directory (expected: Shah1Tunnel-Core) and runs its setup script: bash "$SRC/setup.sh" "$@"
- The installer sets PACKAGE_CLEANUP=1 and will remove extracted installer files and (optionally) the ZIP found in /root that exactly matches the package.

## Usage
- To run the installer: sudo bash S1T-START.sh [options]
- The installer accepts options forwarded to the underlying setup.sh in the extracted package. Check the extracted package's setup.sh for available options.

## Troubleshooting / common errors
- "package files are missing." — means one of: S1T-CHECKS.sha256, the expected archive (S1T-CAPSULE/S1T-SOURCE.tar.gz) or S1T-INFO-FA.txt is missing from the directory where S1T-START.sh is placed.
  - Fix: Ensure you cloned the repository root and the files are present.
- "package integrity check failed; nothing was executed." — sha256sum verification failed. Do not proceed. Verify the files manually or re-download the repository.
- "cannot allocate workspace with at least 16 MiB free" — ensure a writable directory with enough free space exists. You can set WAT_TMPDIR to a path with adequate free space before running the script, e.g. `WAT_TMPDIR=/var/tmp sudo bash S1T-START.sh`.
- "invalid encoded source path." or "source filename collision detected." — indicates an unexpected or unsafe filename inside the capsule; do not proceed and report the exact error and file list to the project maintainer.

## Security and safety notes
- The installer unpacks and executes code from an embedded archive. Only run it on trusted hosts and review S1T-CHECKS.sha256 and S1T-INFO-FA.txt before executing.
- The script may remove installer files from the directory where it runs (cleanup). It will only remove files that match the exact onboarding artifacts — unrelated files are preserved per the cleanup logic.
- The installer may search /root for ZIP files that match the manifest and remove them if PACKAGE_CLEANUP is enabled. Be aware of that behavior on multi-user systems.

## Known repository quirk
- In this repository the archive inside S1T-CAPSULE is present as a file named `null` when listed via the GitHub API. The installer expects `S1T-CAPSULE/S1T-SOURCE.tar.gz`. To run the installer after cloning, either:
  - create a symlink: `ln -sf $(pwd)/S1T-CAPSULE/null $(pwd)/S1T-CAPSULE/S1T-SOURCE.tar.gz`, or
  - rename the file: `mv S1T-CAPSULE/null S1T-CAPSULE/S1T-SOURCE.tar.gz` (rename will change repo content; prefer symlink)

---

# فارسی

## دربارهٔ پروژه
Shah1Tunnel یک مدیر و نصب‌کنندهٔ بسته است که یک کپسول منبع تعبیه‌شده (موتور WatTunnel نسخهٔ v2.4.12) را بازسازی کرده و نصب باینری رسمی را اجرا می‌کند. اسکریپت اصلی نصب `S1T-START.sh` بررسی‌های یکپارچگی را انجام داده، فضای کاری ایجاد می‌کند، کپسول را استخراج می‌کند و اسکریپت‌های راه‌اندازی بسته را اجرا می‌کند.

### پشته
- زبان(ها): اسکریپت‌های شل (Bash)؛ سورس درون بسته نشان می‌دهد پروژه با Go نوشته شده است (وجود go.mod و cmd/*/*.go)
- نیاز اجرا: bash + coreutils + tar + base64
- اجزأ مهم: S1T-START.sh (نصب‌کننده)، S1T-CHECKS.sha256 (نمایهٔ یکپارچگی)، S1T-CAPSULE/* (کپسول تعبیه‌شده)

## ساختار بالاترین سطح
```
README.md
S1T-START.sh
S1T-CHECKS.sha256
S1T-INFO-FA.txt
S1T-CAPSULE/
```

## پیش‌نیازها
- سیستم یونیکس‌مانند با bash، tar، sha256sum، base64 و ابزارهای پایه
- حداقل 16 MiB فضای آزاد روی یکی از مسیرهای WAT_TMPDIR, /var/tmp, /root/.cache/shah1tun یا /tmp

## نصب (خلاصه)
1. کپی یا clone مخزن
2. وارد پوشهٔ Shah1Tunnel شوید
3. مطمئن شوید S1T-CHECKS.sha256 و محتوای S1T-CAPSULE در ریشهٔ مخزن موجود است
4. اجرای: `sudo bash S1T-START.sh`

## عملکرد نصب‌کننده (جزئیات مهم)
- نصب‌کننده ابتدا صحت فایل‌ها را با `sha256sum --status -c S1T-CHECKS.sha256` بررسی می‌کند.
- فضای کاری دیسکی رزرو شده و از tmpfs‌های کوچک استفاده نمی‌شود.
- پس از استخراج کپسول، اسکریپت setup.sh داخل پوشهٔ بازیابی شده اجرا می‌شود.
- نصب‌کننده پس از پایان (با PACKAGE_CLEANUP=1) فایل‌های نصب استخراج‌شده را حذف می‌کند و ممکن است آرشیو ZIP مطابق را از /root پاک کند.

## استفاده
- برای اجرای نصب‌کننده: `sudo bash S1T-START.sh [options]`
- گزینه‌ها به setup.sh در بستهٔ استخراج‌شده پاس داده می‌شوند — برای فهرست گزینه‌ها باید setup.sh را بررسی کنید.

## رفع خطاهای رایج
- "package files are missing." → فایل S1T-CHECKS.sha256، آرشیو یا S1T-INFO-FA.txt در کنار S1T-START.sh وجود ندارد.
- "package integrity check failed; nothing was executed." → اشکال در تطبیق SHA256؛ پیش از ادامه فایل‌ها را بررسی کنید.
- "cannot allocate workspace with at least 16 MiB free" → مطمئن شوید مسیر قابل نوشتن با فضای کافی موجود است یا WAT_TMPDIR را تنظیم کنید.

## نکات امنیتی
- اسکریپت کد را از آرشیو استخراج و اجرا می‌کند. تنها روی ماشین‌های مورد اعتماد اجرا کنید.
- حذف فایل‌های نصب پس از اجرا (cleanup) ممکن است رخ دهد؛ اسکریپت سعی می‌کند فقط فایل‌های هم‌پک را حذف کند.

## نکتهٔ شناخته‌شده در مخزن
- در این مخزن، فایل آرشیو داخل S1T-CAPSULE با نام `null` فهرست شده است اما اسکریپت `S1T-START.sh` به دنبال `S1T-CAPSULE/S1T-SOURCE.tar.gz` می‌گردد. برای اجرای مستقیم پس از clone، از ایجاد یک symlink استفاده کنید:
```
ln -sf $(pwd)/S1T-CAPSULE/null $(pwd)/S1T-CAPSULE/S1T-SOURCE.tar.gz
```

---

If you want, I will commit this README to the repository now. I will not modify any other files unless you explicitly ask me to (for example to add a small compatibility change to S1T-START.sh that accepts `S1T-CAPSULE/null` automatically).