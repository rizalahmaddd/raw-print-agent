# Kebijakan Keamanan

RawPrint Agent membuka port HTTP lokal, jadi laporan keamanan ditangani dengan serius.

## Melaporkan celah keamanan

Mohon **jangan** melaporkan celah keamanan lewat issue publik.

Laporkan secara privat lewat tab **Security → Report a vulnerability** di repo ini (GitHub Private Vulnerability Reporting). Sertakan:

- jenis celah dan dampaknya,
- langkah untuk mereproduksi,
- versi atau commit yang terdampak.

Laporan akan ditanggapi dalam waktu seminggu. Setelah perbaikan dirilis, pelapor akan dicantumkan di catatan rilis kalau berkenan.

## Cakupan

**Termasuk:** situs web berbahaya atau yang disusupi di browser pengguna yang mencoba mencetak, membaca informasi printer, melakukan pairing sendiri, atau mengubah pengaturan. Contohnya CORS, pemeriksaan Origin, DNS rebinding, CSRF ke halaman pengaturan, dan pemakaian ulang token lintas origin.

**Tidak termasuk:** program yang sudah berjalan di PC Windows yang sama. Program seperti itu bisa langsung mengakses spooler tanpa melalui agent.

## Versi yang didukung

Perbaikan keamanan hanya dirilis untuk versi terbaru.
