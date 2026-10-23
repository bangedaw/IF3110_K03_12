# Keputusan Desain

Keputusan yang diminta spesifikasi untuk ditentukan dan didokumentasikan tim, beserta cara
implementasinya. Arsitektur dan alur request ada di [architecture.md](architecture.md).

## 1. Frontend: React SPA

Spesifikasi: *"Frontend dibebaskan. Tampilan dapat dibuat menggunakan Go template (bukan SPA)."*
Tafsiran yang dipakai: frontend bebas; Go template hanya salah satu opsi. Frontend dibuat sebagai
React + TypeScript SPA yang memanggil backend Go lewat JSON API. Untuk demo/production, hasil build SPA
di-embed ke binary Go sehingga SPA dan API berada di satu origin (tanpa CORS).

## 2. Authentication

- Login dengan username + password. Password disimpan sebagai hash **Argon2id** format PHC
  (m = 19 MiB, t = 2, p = 1, salt 16 byte acak; parameter rekomendasi OWASP).
- Pesan gagal login selalu sama ("username atau password salah") untuk password salah, username tidak
  ada, maupun user nonaktif. Untuk username yang tidak ada tetap dijalankan verifikasi terhadap hash
  dummy agar waktu respons tidak membocorkan keberadaan username.
- Password dan session identifier tidak pernah muncul di URL, body response, log aplikasi, maupun audit
  log. Logger HTTP hanya mencatat method, path (tanpa query string), status, durasi, dan request ID.
- Platform Admin pertama dibuat dengan `dbmpctl create-admin`; password dibaca dari stdin, bukan argumen
  command line.

## 3. Session

| Parameter | Nilai default | Env var |
|---|---|---|
| Idle timeout | 30 menit | `SESSION_IDLE_TIMEOUT` |
| Absolute expiration | 8 jam | `SESSION_ABSOLUTE_TIMEOUT` |
| Interval rotasi periodik | 15 menit | `SESSION_ROTATION_INTERVAL` |
| Grace period identifier lama | 30 detik | `SESSION_GRACE_PERIOD` |
| Throttle update `last_seen_at` | 60 detik | `SESSION_TOUCH_INTERVAL` |

Alasan: 30 menit idle dan 8 jam absolute adalah ukuran wajar untuk aplikasi administratif yang
sensitif (satu hari kerja). Rotasi 15 menit membatasi umur identifier yang mungkin bocor. Grace 30 detik
cukup untuk request paralel atau response rotasi yang hilang, tetapi cukup singkat agar identifier lama
cepat tidak berguna. Throttle 60 detik mengurangi write ke DB, dengan konsekuensi idle timeout akurat
±60 detik. Untuk demo, semua nilai bisa diperpendek lewat env var (mis. idle `1m`, rotasi `20s`).

**Identifier.** 32 byte acak dari `crypto/rand`, di-encode base64url, dan hanya dikirim lewat cookie
`dbmp_session` (`HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` saat `APP_ENV=production`, `Max-Age` =
sisa absolute expiration). Database hanya menyimpan SHA-256 dari identifier, sehingga kebocoran tabel
session tidak langsung memberi akses.

**Family.** Satu login = satu *session family* (`session_families`) yang memegang absolute expiration,
`last_seen_at`, `last_rotated_at`, `rotate_required`, dan `revoked_at`. Setiap identifier
(`session_tokens`) milik satu family; rotasi menambah token baru di family yang sama.

**Pemeriksaan setiap request** (middleware `auth.Middleware`), ditolak dengan 401 jika:
family di-revoke; absolute expiration lewat (`SESSION_EXPIRED`); idle timeout lewat (`SESSION_EXPIRED`);
user dinonaktifkan (family sekaligus di-revoke); atau token sudah digantikan dan grace period-nya lewat.

**Rotasi.** Nama cookie tetap, nilainya diganti. Rotasi terjadi:

- saat login: selalu family baru, cookie lama diabaikan (mencegah session fixation);
- secara periodik: request pertama setelah `SESSION_ROTATION_INTERVAL`;
- saat privilege naik: admin API menandai `rotate_required` pada semua session user ketika user dijadikan
  Platform Admin, ditambahkan ke group, atau group-nya mendapat akses lebih tinggi (tidak ada → view/edit,
  view → edit). Penurunan privilege tidak perlu rotasi karena hak akses dihitung ulang dari database di
  setiap request.

Rotasi atomik: token lama ditandai `superseded_at` hanya jika belum digantikan, sehingga dari beberapa
request paralel hanya satu yang menghasilkan token baru; sisanya dilayani sebagai grace. Token lama yang
masih dalam grace period diterima **tanpa** memicu rotasi lagi. Rotasi tidak pernah mengubah absolute
expiration.

**Trade-off grace period.** Karena server hanya menyimpan hash, server tidak bisa mengirim ulang token
baru kepada pemegang token lama. Jika response rotasi hilang, client masih bisa memakai token lama selama
grace period; setelah itu harus login ulang. Ini disengaja demi kesederhanaan dan keamanan penyimpanan.

**Revokasi di backend.**

| Kejadian | Yang di-revoke |
|---|---|
| Logout | family saat ini |
| User dinonaktifkan, password direset admin, atau admin menekan "Cabut sesi" | semua family user |
| `dbmpctl create-admin` dijalankan ulang untuk username yang sama | semua family user |

## 4. CSRF

Tiga lapis:

1. Cookie `SameSite=Lax`: browser tidak mengirim cookie session pada POST lintas situs.
2. `http.CrossOriginProtection` (stdlib Go ≥ 1.25) di semua route, termasuk `/api/auth/login`: request
   POST/PUT/PATCH/DELETE lintas origin (berdasarkan `Sec-Fetch-Site`/`Origin`) ditolak 403 `CSRF_INVALID`.
3. Synchronizer token `base64url(HMAC-SHA256(CSRF_KEY, family_id))`, dikembalikan di body
   `/api/auth/login` dan `/api/auth/me`. Setiap request non-GET ke route yang dilindungi wajib mengirim
   header `X-CSRF-Token`; dibandingkan constant-time. Token terikat pada family sehingga tetap berlaku
   setelah rotasi session dan tidak perlu disimpan. Frontend menyimpannya di memori (bukan
   `localStorage`) dan menambahkannya otomatis lewat interceptor `fetch`.

## 5. Authorization

Semua pemeriksaan hak akses ada di backend, terpusat di `backend/internal/authz`:

- `authz.go`: daftar aksi (`platform:users:manage`, `db:view`, `migration:execute`, ...).
- `policy.go`: tabel aksi → syarat (Platform Admin, atau level minimum `view`/`edit` pada database).
  Aksi yang tidak terdaftar selalu ditolak.
- `db.go`: menghitung level efektif dari database lalu memutuskan lewat `policy.go`.

Service setiap domain memanggil `Authorize` sebelum bekerja; middleware hanya memastikan request sudah
login. Menyembunyikan tombol di UI hanya kenyamanan, bukan kontrol akses. Matriks lengkap diuji di
`policy_test.go`.

## 6. Akses efektif multi-group

**Akses efektif = tingkat akses tertinggi dari seluruh group user untuk database tersebut**
(`edit` > `view` > tidak ada). Model group bersifat *additive grant*; tidak ada konsep deny.

```sql
SELECT COALESCE(MAX(g.level)::text, '')   -- urutan enum access_level ('view','edit')
FROM group_database_grants g
JOIN group_members m ON m.group_id = g.group_id
WHERE m.user_id = $1 AND g.database_id = $2;
```

Contoh: user di group `analyst` (view pada `inventory`) dan `backend-team` (edit pada `inventory`)
mendapat **edit** pada `inventory`. Perubahan grant atau keanggotaan berlaku pada request berikutnya,
karena level dihitung ulang setiap kali (tanpa cache).

## 7. Platform Admin

- Platform Admin mengelola user, group, koneksi database, dan grant.
- **Platform Admin tidak otomatis memiliki akses View/Edit ke database mana pun.** Untuk melihat data
  atau menjalankan migration, admin harus menjadi anggota group yang memiliki grant (separation of
  duties). Admin tetap boleh menguji koneksi (ping, tanpa membaca data).
- Admin tidak bisa menonaktifkan, mencabut status admin, atau menghapus akunnya sendiri (409), agar
  platform tidak kehilangan admin secara tidak sengaja.
- User yang masih direferensikan riwayat (mis. pembuat migration) tidak bisa dihapus (409); nonaktifkan
  saja agar riwayat tetap utuh.

## 8. Model migration

- Draft dibuat/diubah/dihapus oleh user `edit`; perubahan memakai `revision` (optimistic locking, 409
  jika basi).
- Nomor versi (1, 2, 3, ...) diberikan **saat eksekusi berhasil**, sehingga riwayat applied linear tanpa
  lubang. Migration yang sudah applied tidak bisa diubah; perbaikan lewat migration baru.
- Setiap percobaan eksekusi dicatat sebagai *run*: SQL yang dijalankan, pelaksana, waktu, status, error.
- Eksekusi berjalan dalam transaction: commit jika semua statement berhasil, rollback jika ada yang gagal.
  Hanya satu eksekusi per database pada satu waktu.

## 9. Kredensial database target

Password koneksi dienkripsi AES-256-GCM dengan `DBMP_SECRET_KEY` (32 byte, base64) dari environment.
API tidak pernah mengembalikan password (write-only); saat mengubah koneksi, password kosong berarti tidak
diubah. Platform menjalankan query dengan kredensial yang didaftarkan admin, jadi daftarkan user database
dengan privilege secukupnya.

## 10. Audit log

Tabel `audit_logs` mencatat: login berhasil/gagal (username, tanpa password), setiap aksi admin yang
berhasil, dan setiap penolakan otorisasi dari domain mana pun (aksi + database). Metadata tidak pernah
berisi password, hash, atau token. Penulisan audit bersifat best-effort: kegagalan menulis audit dicatat
di log server dan tidak menggagalkan request.

## 11. Konvensi API

| Konvensi | Keputusan |
|---|---|
| Kontrak | `api/openapi.yaml` (OpenAPI 3.0.3) sumber kebenaran; `make gen` menghasilkan tipe Go dan TypeScript |
| Format | prefix `/api`, JSON `camelCase`, waktu ISO 8601 UTC, ID number (int64) |
| Error | RFC 9457 Problem Details `{type, title, status, detail, code, errors?}`, `application/problem+json` |
| Pagination | `?limit=&offset=` (default 50, maks 200) → `{items, limit, offset, total}` |
| CSRF | header `X-CSRF-Token` untuk POST/PUT/PATCH/DELETE |

| Code | Status |
|---|---|
| `UNAUTHENTICATED`, `SESSION_EXPIRED` | 401 |
| `FORBIDDEN`, `CSRF_INVALID` | 403 |
| `NOT_FOUND` | 404 |
| `CONFLICT` | 409 |
| `VALIDATION_FAILED`, `MIGRATION_FAILED` | 422 |
| `DB_UNREACHABLE` | 503 |
| error tak terduga | 500 `INTERNAL`; detail asli hanya di log server |
