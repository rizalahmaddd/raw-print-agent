# HTTP API

Base URL: `http://127.0.0.1:18310`. Semua body memakai JSON. Bentuk error:

```json
{ "error": { "code": "not_paired", "message": "pair with the agent first (POST /pair)" } }
```

## Aturan akses

- Header `Host` harus `127.0.0.1`, `localhost`, atau `::1` dengan port agent, untuk mencegah DNS rebinding.
- Request dengan header `Origin` ditolak kalau origin-nya tidak ada di `allowed_origins`.
- Endpoint bertanda **token** butuh `X-Agent-Token: <token>` atau `Authorization: Bearer <token>`. Token hanya berlaku dari origin penerimanya.
- Preflight menjawab `Access-Control-Allow-Private-Network: true` untuk Local/Private Network Access di Chrome.
- `/admin/*` dan halaman pengaturan hanya menerima request dari origin agent sendiri.

## Endpoint

### `GET /health`

```json
{ "name": "rawprint-agent", "version": "1.0.0", "paired": true,
  "printer": { "name": "EPSON LX-310 ESC/P", "driver": "EPSON LX-310 ESC/P", "port": "USB001",
               "status": "ready", "offline": false, "jobs": 0 },
  "profile": { "page_height_mm": 279.4, "top_margin_lines": 3, "left_margin_cols": 0, "quality": "draft", "cpi": 12 } }
```

`printer` dan `profile` hanya disertakan untuk pemanggil yang sudah pairing.

### `POST /pair`

Body: `{ "name": "Kasir Toko - Gudang 1" }`. Harus berasal dari origin terdaftar.

- Persetujuan otomatis aktif: `200 { "status": "approved", "token": "...", "token_id": "..." }`
- Selain itu: `202 { "status": "pending", "request_id": "...", "code": "123456", "approve_url": "http://127.0.0.1:18310/#pair", "expires_in": 600 }`

Tampilkan kode ke pengguna, lalu pengguna menyetujuinya di halaman pengaturan.

### `GET /pair/{request_id}`

`{ "status": "pending" | "approved" | "rejected" }`. Saat disetujui, respons berisi `token` tepat satu kali.

### `GET /printers` (token)

`{ "printers": [ ... ], "default": "EPSON LX-310 ESC/P" }`

### `GET /profile?printer=NAME` / `PUT /profile` (token)

Body `PUT`: `{ "printer": "", "profile": { "page_height_mm": 279.4, "top_margin_lines": 3, "left_margin_cols": 0, "quality": "draft", "cpi": 12 } }`.

`printer` kosong berarti printer bawaan. Batas nilai: tinggi 50–600 mm, atas 0–20, kiri 0–40, quality `draft|nlq`, cpi `10|12|17`.

Profil ini disimpan per printer agar aplikasimu bisa menyusun dokumen untuk PC tersebut. Agent sendiri hanya memakainya untuk halaman uji.

### `POST /print` (token)

```json
{ "job_id": "stock-issue-123-7f3a", "doc_name": "SI-2611-00012", "printer": "",
  "data_base64": "G0AbeAAb...", "copies": 1 }
```

- `job_id` wajib, maksimal 128 karakter. `job_id` yang sama dalam 10 menit mengembalikan job pertama dengan `"duplicate": true` dan tidak dicetak lagi, kecuali job itu gagal.
- `copies` 1–10. Data diulang di dalam satu job spooler.
- Batas data: `max_payload_bytes`, bawaan 2 MiB setelah decode base64.

`202 { "job": { "job_id": "...", "printer": "...", "state": "queued", "spool_job_id": 17, ... }, "duplicate": false }`

`502` dengan `job.state = "error"` kalau spooler menolak job.

### `GET /jobs/{job_id}` (token)

`{ "job": { ..., "state": "queued" | "printing" | "done" | "error", "detail": "paper out" } }`

Status dibaca dari Windows spooler selama job belum selesai. Job disimpan 24 jam.

### `POST /test-print` (token)

Body opsional: `{ "printer": "" }`. Mencetak halaman kalibrasi ESC/P memakai profil printer tersebut.
