# Keputusan Desain

Keputusan yang diminta spesifikasi untuk ditentukan dan didokumentasikan tim, beserta cara
implementasinya. Arsitektur dan alur request ada di [architecture.md](architecture.md).

## 1. Frontend: server-rendered, bukan SPA

Spesifikasi: *"Frontend dibebaskan. Tampilan dapat dibuat menggunakan Go template (bukan SPA)."*
Kalimat ini ditanyakan ke asisten lewat Q&A. Jawabannya: *"untuk milestone 1, frontendnya tidak SPA,
go templating disitu jadi contohnya."* Karena itu tampilan dirender di server dengan `html/template`:

- Setiap halaman adalah route `GET` biasa; setiap aksi adalah form `POST` yang diakhiri redirect 303
  (Post/Redirect/Get). Tidak ada routing atau rendering sisi klien, dan tidak ada JavaScript.
- Template dan aset statis di-embed ke binary (`embed.FS`), sehingga halaman dan API berada di satu origin.
- Layer service tidak tahu HTTP; handler halaman dan handler JSON memanggil service yang sama.
- Kekurangan yang disengaja: editor SQL berupa `<textarea>` tanpa syntax highlighting, dan setiap aksi
  memuat ulang halaman.
- JSON API di `/api` (dengan kontrak `api/openapi.yaml`) tetap tersedia dan teruji, tetapi tidak dipakai
  oleh halaman.

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

**Pemeriksaan setiap request** (`auth.Middleware` untuk JSON API, `auth.WebAuthenticate` untuk halaman),
ditolak jika (JSON API: 401; halaman: redirect 303 ke `/login?next=...`, dengan `reason=expired` bila sesi berakhir):
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
3. Synchronizer token `base64url(HMAC-SHA256(CSRF_KEY, family_id))`. Pada halaman, token ditanam sebagai
   field tersembunyi `_csrf` di setiap form POST dan dibandingkan constant-time; token salah atau kosong
   menghasilkan 403. Pada JSON API token dikembalikan di body `/api/auth/login` dan `/api/auth/me` dan
   dikirim lewat header `X-CSRF-Token`. Token terikat pada family sehingga tetap berlaku setelah rotasi
   session dan tidak perlu disimpan.

Form login tidak memakai token (belum ada session); ia dilindungi `SameSite=Lax` dan
`CrossOriginProtection`, dan parameter `next` hanya menerima path lokal.

## 5. Authorization

Semua pemeriksaan hak akses ada di backend, terpusat di `backend/internal/authz`:

- `authz.go`: daftar aksi (`platform:users:manage`, `db:view`, `migration:execute`, ...).
- `policy.go`: tabel aksi → syarat (Platform Admin, atau level minimum `view`/`edit` pada database).
  Aksi yang tidak terdaftar selalu ditolak.
- `db.go`: menghitung level efektif dari database lalu memutuskan lewat `policy.go`.

Service setiap domain memanggil `Authorize` sebelum bekerja; middleware hanya memastikan request sudah
login. Menyembunyikan tombol di template hanya kenyamanan, bukan kontrol akses; POST langsung tetap
ditolak 403. Matriks lengkap diuji di
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
  Run yang gagal mencatat nomor statement yang gagal beserta pesan error database.
- Hanya satu eksekusi per database pada satu waktu: advisory lock di platform DB (eksekusi kedua langsung
  ditolak 409) dan di database target.
- Jika versi database berubah sejak draft dibuat (`base_version` ≠ versi saat ini), eksekusi ditolak 409
  sampai pengguna mengonfirmasi bahwa SQL sudah ditinjau ulang.
- Statement pengendali transaksi (`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`) dan statement yang tidak
  boleh berada dalam transaction PostgreSQL (`VACUUM`, `CREATE INDEX CONCURRENTLY`, `CREATE DATABASE`)
  ditolak saat draft disimpan (422).
- Script yang kosong atau hanya berisi komentar/spasi ditolak (422) karena tidak ada statement yang dijalankan.
- Database target menyimpan tabel `_dbmp_schema_history` yang ditulis dalam transaction yang sama dengan
  perubahan migration. Saat server start (sebelum menerima request), *reconciler* memeriksa run yang masih
  berstatus `running` dari proses sebelumnya: bila tercatat di `_dbmp_schema_history`, run ditandai
  berhasil dan migration diberi versinya; bila tidak, run ditandai gagal (`RECONCILED`). Run pada target
  yang tidak terjangkau dibiarkan `running` dan diperiksa lagi pada start berikutnya.
- Setelah eksekusi berhasil, snapshot schema disimpan dengan referensi ke run-nya (dipakai schema diff).
- EXPLAIN / EXPLAIN ANALYZE (butuh `edit`): PostgreSQL menjalankan seluruh script dalam transaction yang
  selalu di-rollback; DDL dijalankan hanya sebagai konteks. Plan ditampilkan sebagai pohon node (jenis
  node, relasi, index, kondisi join, cost, estimasi vs aktual baris, waktu, loop), dengan penanda node
  termahal dan Seq Scan pada tabel besar. Nilai sequence yang terpakai saat ANALYZE tidak ikut di-rollback.

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

## 11. Konvensi halaman dan API

**Halaman (utama).**

| Konvensi | Keputusan |
|---|---|
| Metode | `GET` membaca, `POST` untuk semua mutasi (form HTML hanya mendukung dua metode ini) |
| Setelah mutasi | redirect 303 dengan pesan flash (cookie sekali-baca) |
| Validasi gagal | 422, form yang sama dirender ulang dengan pesan per field; password tidak diisi ulang |
| Tidak login / sesi berakhir | 303 ke `/login?next=...` |
| Tidak berhak / konflik / tidak ada / DB target mati | 403 / 409 / 404 / 503 (halaman error, bukan 500) |
| Aksi destruktif | halaman konfirmasi (`GET`) lalu `POST` |
| Pagination | `?limit=&offset=` (default 50, maks 200); sort `?orderBy=&orderDir=` |
| Escaping | `html/template`; CSP `default-src 'self'` |

**JSON API (`/api`, tidak dipakai halaman).**

| Konvensi | Keputusan |
|---|---|
| Kontrak | `api/openapi.yaml` (OpenAPI 3.0.3); `make gen` menghasilkan tipe Go |
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

## 12. Dukungan MySQL

- Di MySQL, "schema" sama dengan nama database yang terhubung, jadi explorer menampilkan satu schema.
  Introspeksi memakai `information_schema`; viewer memakai `START TRANSACTION READ ONLY`.
- DDL MySQL melakukan *implicit commit* sehingga tidak bisa di-rollback. Migration MySQL bersifat
  non-transaksional untuk DDL: kegagalan di tengah script dapat meninggalkan perubahan DDL parsial, dan
  halaman eksekusi memperingatkan hal ini sebelum dijalankan. Statement DML tetap dijalankan dalam
  transaction sehingga migration yang hanya berisi DML di-rollback penuh saat gagal.
- EXPLAIN MySQL tidak pernah menjalankan DDL (DDL ditandai "tidak dijalankan"), dan EXPLAIN ANALYZE
  ditolak untuk script yang mengandung DDL. Plan MySQL ditampilkan sebagai JSON (atau teks pohon untuk
  ANALYZE).

## 13. Schema diff

- Pembanding: database lain yang terdaftar (butuh `view` di kedua sisi) atau snapshot tersimpan dari
  database yang sama. Hanya arah `toTarget`: SQL yang dihasilkan membuat database yang dipilih menyamai
  pembanding. Halaman hanya menawarkan pembanding dengan jenis DBMS yang sama karena SQL dihasilkan per dialek.
- Menyimpan hasil sebagai draft migration butuh `edit` pada database yang diubah.
- Rename tidak terdeteksi otomatis: muncul sebagai drop + add dan ditandai **destructive**. Perubahan tipe
  kolom bisa gagal jika data tidak kompatibel. SQL hanyalah kandidat; pengguna meninjau dan dapat
  mengubahnya di `<textarea>` sebelum disimpan.
- Ekspresi default dinormalisasi sebelum dibandingkan (cast PostgreSQL dibuang, `now()` disamakan dengan
  `CURRENT_TIMESTAMP`) agar perbedaan penulisan tidak dianggap perubahan.
- Urutan SQL aman: drop constraint → drop index → drop/alter kolom → drop tabel → create tabel → add kolom
  → add constraint dan index.
