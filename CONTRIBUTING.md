# Contributing to `gvm` (`g`)

Cảm ơn bạn đã quan tâm và đóng góp cho **`gvm` (`g`)** — Cross-Platform Golang Version Manager!  
Thank you for your interest in contributing to **`gvm` (`g`)**!

---

## 1. Ways to Contribute / Các hình thức đóng góp

- **Báo lỗi (Bug Reports)**: Tạo Issue mới qua mẫu [Bug Report](https://github.com/ducconit/gvm/issues/new?template=bug_report.yml) kèm thông tin hệ điều hành (`g -v`, `g env`) và các bước tái hiện.
- **Đề xuất tính năng (Feature Requests)**: Tạo Issue mới qua mẫu [Feature Request](https://github.com/ducconit/gvm/issues/new?template=feature_request.yml).
- **Gửi Pull Request (Code & Documentation)**: Cải thiện hiệu năng, hỗ trợ thêm IDE cho `g skills install`, bổ sung MCP tools, hoặc hoàn thiện tài liệu.

---

## 2. Development Setup / Thiết lập môi trường phát triển

### Yêu cầu / Prerequisites
- **Go**: `>= 1.24` (khuyến nghị `Go 1.27+`)
- **Git** & **Make**

### Clone & Build

```bash
git clone https://github.com/ducconit/gvm.git
cd gvm

# Chạy kiểm tra static analysis (go vet) và unit/integration tests với race detector
make vet test

# Biên dịch binary cho máy hiện tại (xuất ra ./bin/g)
make build

# Biên dịch chéo (Cross-compile) cho macOS, Linux, Windows (amd64 & arm64)
make cross-build
```

---

## 3. Project Structure / Cấu trúc thư mục

```text
.
├── cmd/g/                  # Entrypoint của CLI binary `g`
├── internal/
│   ├── cli/                # Cobra commands (`install`, `use`, `alias`, `ls`, `uninstall`, `update`, `env`, `mcp`, `skills`)
│   ├── goversion/          # Client `go.dev/dl/?mode=json`, SHA256 verifier & SemVer resolver
│   ├── manager/            # Business logic điều phối cài đặt, chuyển đổi, xoá phiên bản
│   ├── mcp/                # Model Context Protocol (MCP) JSON-RPC 2.0 stdio server
│   ├── platform/           # OS/Arch detection, Tar-Slip/Zip-Slip safe extractor, symlink/junction switcher
│   ├── skills/             # AI Agent Skills installer (`SKILL.md` embedded)
│   ├── store/              # Quản lý trạng thái cục bộ trong `$GVM_HOME` (`versions/`, `aliases.json`, `current`)
│   ├── updater/            # Tự động cập nhật phiên bản CLI (`g update [version]`)
│   └── version/            # Metadata phiên bản build
├── assets/                 # Banner / OG Image (`banner.svg`, `banner.png`)
├── install.sh              # One-liner installer cho macOS & Linux
└── install.ps1             # One-liner installer cho Windows PowerShell
```

---

## 4. Coding & Testing Standards / Tiêu chuẩn mã nguồn

1. **Zero External Runtime Dependencies**: Binary biên dịch với `CGO_ENABLED=0`, chạy độc lập trên macOS, Linux và Windows.
2. **Security First**:
   - Mọi thao tác giải nén (`.tar.gz`, `.zip`) phải chống **Tar-Slip / Zip-Slip** và symlink escape.
   - Mọi bản tải về từ `go.dev/dl` phải được đối chiếu mã băm **SHA256**.
3. **Testing**:
   - Mọi tính năng mới hoặc bản vá lỗi đều cần đi kèm unit test (`*_test.go`).
   - Sử dụng `t.TempDir()` và `httptest.Server` trong test — tuyệt đối không ghi đè vào thư mục `~/.gvm` thật của máy dev khi chạy test.
   - Đảm bảo `go vet ./...` và `go test -v -race -count=1 ./...` đạt **100% PASS** trước khi mở Pull Request.

---

## 5. Pull Request Process / Quy trình gửi PR

1. Fork repository [`ducconit/gvm`](https://github.com/ducconit/gvm) và tạo branch mới từ `main` (`git checkout -b feat/ten-tinh-nang` hoặc `fix/ten-loi`).
2. Viết code và bổ sung unit test tương ứng.
3. Chạy `make all` (`go vet`, `go test -race`, `go build`) để kiểm tra.
4. Mở Pull Request hướng vào nhánh `main` và điền đầy đủ thông tin theo mẫu PR.
