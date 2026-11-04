# Panduan Instalasi

Butuh Windows 10/11, 64-bit atau 32-bit, dan hak Administrator saat instalasi.

## File rilis

| File | Kegunaan |
|---|---|
| `RawPrintAgent-Setup.exe` | **Pakai yang ini.** Installer yang otomatis memilih 64-bit atau 32-bit |
| `RawPrintAgent-Setup-<versi>.exe` | Installer yang sama, dengan nomor versi di namanya |
| `rawprint-agent-amd64.exe`, `rawprint-agent-386.exe` | Program tanpa installer, untuk pemasangan lewat [CLI](#cli) |
| `SHA256SUMS.txt` | Checksum untuk memastikan file tidak berubah |

## Instalasi

1. Unduh dan jalankan [RawPrintAgent-Setup.exe](https://github.com/rizalahmaddd/raw-print-agent/releases/latest/download/RawPrintAgent-Setup.exe). Kalau SmartScreen memperingatkan karena installer belum ditandatangani, pilih **More info → Run anyway**.
2. Isi origin aplikasi web yang boleh mencetak, misalnya `https://pos.example.com`, dan bagian dari nama printer kalau perlu, misalnya `LX-310`. Keduanya bisa diisi nanti.
3. Di halaman pengaturan yang terbuka, pilih printer dan ukuran kertas, lalu cetak halaman uji.

Halaman pengaturan juga ada di **Start menu → RawPrint Agent Settings** (`http://127.0.0.1:18310/`).

**Upgrade:** jalankan setup versi baru. Pengaturan dan pairing tetap tersimpan.

**Uninstall:** lewat Settings → Apps di Windows. Pengaturan dan log tetap ada di `%ProgramData%\RawPrintAgent`; hapus folder itu kalau ingin bersih total.

## Instalasi massal

```bat
RawPrintAgent-Setup.exe /VERYSILENT /ORIGINS="https://pos.example.com" /PRINTERMATCH="LX-310"
```

| Parameter | Bawaan | Fungsi |
|---|---|---|
| `/ORIGINS` | kosong | Origin web yang boleh mencetak, dipisah koma |
| `/PRINTERMATCH` | kosong | Pakai printer pertama yang nama atau driver-nya mengandung teks ini |
| `/AUTOAPPROVE=1` | mati | Pairing dari origin terdaftar disetujui tanpa konfirmasi |
| `/BROWSERPOLICY=0` | aktif | Jangan tambahkan `/ORIGINS` ke kebijakan Local Network Access di Chrome dan Edge |
| `/PORT` | `18310` | Port loopback. Ubah hanya kalau 18310 sudah dipakai program lain |

## Pengaturan printer

- Pakai **driver v3 (GDI)** seperti driver Epson LX-310 ESC/P, atau *Generic / Text Only*. Driver v4/XPS menolak data RAW.
- Di kertas continuous, matikan *auto tear-off* di printer agar kertas tidak bergeser di antara job.
- Untuk kertas rangkap, atur tuas ketebalan sesuai jumlah lembar dan pakai kualitas Draft.

## Jalan otomatis

Agent otomatis berjalan saat Windows menyala, sebelum ada yang login. Untuk mengubahnya, buka halaman pengaturan, mode **Lanjutan**, kartu **Saat Windows menyala**.

Kalau dimatikan, service menjadi `Manual`. Setelah restart, aplikasi web tidak bisa mencetak dan halaman pengaturan tidak terbuka sampai **RawPrint Agent** dinyalakan dari Services di Windows. Pilihan ini tetap dipakai saat upgrade.

## Solusi masalah

| Masalah | Solusi |
|---|---|
| Halaman pengaturan tidak terbuka | Nyalakan **RawPrint Agent** dari Services di Windows, lalu periksa log. Kalau selalu mati setelah restart, aktifkan jalan otomatis |
| Port sudah terpakai | Jalankan setup lagi dengan `/PORT=NNNNN`, lalu pakai port yang sama di aplikasimu: `new RawPrint({ baseUrl: 'http://127.0.0.1:NNNNN' })` |
| Error `origin_not_allowed` | Tambahkan origin di mode Lanjutan, kartu **Akses aplikasi web** |
| Error `StartDocPrinter (RAW)` | Driver-nya v4/XPS. Ganti ke driver v3 atau *Generic / Text Only* |
| Job terus `queued` | Biasanya printer mati, offline, atau kertas habis |
| Browser meminta izin akses jaringan lokal | Izinkan sekali per situs, atau pasang dengan `/ORIGINS` agar kebijakan Chrome dan Edge terisi |

## Lokasi file

| Isi | Lokasi |
|---|---|
| Konfigurasi | `%ProgramData%\RawPrintAgent\config.json` |
| Log (disimpan 7 hari) | `%ProgramData%\RawPrintAgent\logs` |

## CLI

```
rawprint-agent install   [-config FILE] [-origins URL,URL] [-printer-match TEXT] [-auto-approve] [-browser-policy] [-port N]
rawprint-agent uninstall
rawprint-agent start | stop
rawprint-agent run       [-config FILE] [-file-spool DIR] [-file-printers "A,B"]
rawprint-agent config    [-config FILE]
rawprint-agent version
```

`install`, `uninstall`, `start`, dan `stop` harus dijalankan sebagai Administrator.
