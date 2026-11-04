# Berkontribusi

Terima kasih sudah tertarik berkontribusi ke RawPrint Agent!

## Alur

1. Buka issue dulu untuk perubahan besar, supaya arahnya disepakati sebelum mulai coding.
2. Fork repo, buat branch dari `main`.
3. Jalankan project di lokal mengikuti [docs/getting-started.md](./docs/getting-started.md).
4. Pastikan semua cek lolos sebelum membuat pull request:

   ```bash
   make test
   ```

5. Kalau mengubah perilaku service atau spooler, sebutkan hasil tesnya di Windows sungguhan, karena `make test` di macOS dan Linux tidak menjalankan kode itu.

## Panduan kode

- Agent harus tetap netral terhadap protokol: byte diteruskan tanpa diubah. Tata letak dokumen adalah urusan aplikasi klien.
- Kode khusus Windows berada di balik `//go:build windows`, dengan stub untuk OS lain agar project tetap bisa di-build dan dites di macOS dan Linux.
- Jaga dependensi seminimal mungkin (saat ini hanya `golang.org/x/sys`).
- Perubahan HTTP API harus disertai pembaruan `docs/api.md` dan `clients/js/rawprint.js`.
- Teks di halaman pengaturan tersedia dalam Bahasa Indonesia dan Inggris. Perbarui keduanya.
- Tulis komentar hanya untuk menjelaskan *alasan*, bukan mengulang apa yang dilakukan kode.

## Melaporkan bug

Gunakan template **Laporan bug** saat membuat issue. Untuk celah keamanan, ikuti [SECURITY.md](./SECURITY.md).
