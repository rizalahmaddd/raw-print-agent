# Arsitektur

## Stack

- Go, dengan `golang.org/x/sys` sebagai satu-satunya dependensi
- Windows Service dan Windows spooler (`winspool.drv`)
- Halaman pengaturan: satu file HTML tanpa framework, ditanam di dalam program
- Inno Setup untuk installer, GitHub Actions untuk CI dan rilis

## Struktur folder

```
cmd/rawprint-agent/    entry point dan perintah CLI
internal/
  api/                 server HTTP, pairing, API admin, dan halaman pengaturan (ui/index.html)
  config/              config.json, origin, token, dan profil kertas
  escp/                halaman uji kalibrasi
  jobs/                antrean job, anti cetak ganda, dan status job
  logging/             log harian dengan rotasi
  spool/               Windows spooler, plus penulis file .prn untuk development
  winsvc/              pemasangan service, jalan otomatis, dan kebijakan browser
clients/js/            klien browser (rawprint.js)
installer/             skrip Inno Setup
scripts/               build installer di CI
docs/                  dokumentasi
```

Kode khusus Windows berada di balik `//go:build windows` dan punya pasangan stub, sehingga project tetap bisa di-build dan dites di macOS dan Linux.

## Alur cetak

1. Aplikasi web memanggil `POST /pair`. Origin harus terdaftar, lalu pengguna menyetujui kode di halaman pengaturan dan aplikasi menerima token.
2. Aplikasi mengirim `POST /print` dengan token, `job_id`, dan data base64.
3. Agent meneruskan byte apa adanya ke Windows spooler dengan datatype `RAW`.
4. Aplikasi membaca status lewat `GET /jobs/{job_id}`, yang diambil dari spooler selama job belum selesai.

## Model akses

| Pemanggil | Cara masuk | Cakupan |
|---|---|---|
| Aplikasi web | Origin terdaftar + token pairing | Cetak, printer, dan profil kertas |
| Halaman pengaturan | Origin milik agent sendiri | Semua pengaturan lewat `/admin/*` |
| Program lokal tanpa `Origin` | Token untuk endpoint bertoken, tanpa token untuk `/admin/*` | Di luar model ancaman, lihat [SECURITY.md](../SECURITY.md) |

## Aturan keamanan yang ditegakkan

- Agent hanya mendengarkan di loopback.
- Header `Host` harus `127.0.0.1`, `localhost`, atau `::1` dengan port agent, untuk mencegah DNS rebinding.
- Request dengan `Origin` di luar allowlist ditolak. Token hanya berlaku dari origin penerimanya.
- `/admin/*` menolak origin lain dan request `Sec-Fetch-Site` lintas situs, serta memakai CSP dan `X-Frame-Options: DENY`.
- Token disimpan sebagai hash SHA-256 dan dibandingkan dengan constant-time compare.
- Permintaan pairing kedaluwarsa dalam 10 menit, maksimal 10 yang terbuka sekaligus.
- Data cetak dibatasi `max_payload_bytes`, bawaan 2 MiB.

## Service Windows

Service `RawPrintAgent` berjalan sebagai LocalSystem dengan tipe startup `Automatic`. Kalau berhenti karena error, Windows menyalakannya lagi setelah 5 detik, 15 detik, lalu 60 detik.

Saat jalan otomatis dimatikan, tipe startup menjadi `Manual`, bukan `Disabled`, agar service masih bisa dinyalakan manual dan pemulihan tetap berjalan. Status ini dibaca langsung dari Windows, bukan dari `config.json`.
