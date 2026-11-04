# Memulai Development

Panduan menjalankan agent dari source. Bisa dikerjakan di Windows, macOS, atau Linux. `make run` menulis setiap job sebagai file `.prn`, jadi tidak perlu printer.

## Prasyarat

- Go 1.26+
- `make`
- Inno Setup, hanya untuk membangun installer di Windows

## Setup

```bash
git clone https://github.com/rizalahmaddd/raw-print-agent.git
cd raw-print-agent
make run
```

Buka `http://127.0.0.1:18310/` untuk melihat halaman pengaturan. Job hasil cetak ditulis ke folder `out/`.

## Mencoba dari halaman web

`make run` membuat `dev/config.json` yang di-ignore git. Tambahkan origin halamanmu di `allowed_origins`, lalu jalankan ulang:

```json
{ "allowed_origins": ["http://localhost:5173"] }
```

Pakai [`clients/js/rawprint.js`](../clients/js/rawprint.js) atau panggil [HTTP API](./api.md) langsung.

## Perintah

| Perintah | Fungsi |
|---|---|
| `make test` | Vet untuk OS lokal dan Windows, lalu test dengan race detector |
| `make run` | Agent di `127.0.0.1:18310`, job ditulis ke `out/` |
| `make build` | Build untuk OS lokal ke `dist/` |
| `make windows` | Build `dist/rawprint-agent-amd64.exe` dan `-386.exe` |
| `make clean` | Hapus `dist/` dan `out/` |

## Membangun installer

Di Windows, setelah `make windows`:

```bat
iscc /DAppVersion=1.0.0 installer\rawprint.iss
```

## Merilis versi baru

Push tag, atau jalankan workflow *release* di GitHub Actions dengan nomor versi:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

Versi dengan akhiran seperti `1.2.3-rc.1` menjadi pre-release. Tambahkan secret `SIGN_PFX_BASE64` dan `SIGN_PFX_PASSWORD` agar program dan installer ditandatangani.
