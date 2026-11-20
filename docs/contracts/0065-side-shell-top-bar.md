# 0065 Side-only shell gets the top bar

Status: implemented (18 Nov 2026), amended 20 Nov 2026 · Owner of the change: rangka-os (web app) · Affects: nothing in rangka-apps or rangka-cloud

## Context

Contract 0019 made `side-only` the default shell. In that shell the desktop had no top bar: the TopNav (menu, search,
notifications, help, account) drew only for `shell: top-nav`, or for a layout with `nav.top` and no `shell`.

A project built by Rangka Cloud never starts with either. A site with apps gets no layout, so the kernel falls back to
`side-only`. A site without apps gets the generated `layouts/main.rk`, which has `nav.side` and no `nav.top`. The top
bar only showed up once a later change set added `nav.top`. To owners this looks like a bug: after the first build the
top bar is missing, and later it appears.

Switching the default to `top-nav` is not the fix. The side-only sidebar carries the business switcher, Beranda,
Mulai di sini and the Kenalan tour's targets (`.rk-side-switch`, the nav entries). The juragan bottom tabs also depend
on `side-only`, and the tour does not start in `top-nav`.

## Decision

1. **The side-only shell draws a top bar from 768px up.** It is the design system's `TopNav`, placed in
   `.rk-shell-main` above the page, beside the sidebar (`.ap-dtop`). The sidebar stays exactly as it is.
2. **Contents.** The bar has the app switcher (only when apps are installed), the menu items, search (⌘K), notifications,
   help and the account menu. The product tile is hidden because the sidebar's switcher already names the business. The
   items follow `topNavItems`: the layout's `nav.top` when it has one, else one item per sidebar group, at most 6. Below
   1024px the items hide, as in `top-nav`.
3. **Phone and drawer tablets keep the 48px bar.** At 767px and below, and on a tablet with
   `layout.responsive.tablet: drawer`, `.ap-mhead` shows and the top bar does not.
4. **No schema or kernel change.** `shellKind`, the schema defaults and the kernel fallback stay as contract 0019 has
   them. A layout that names `top-nav` renders as before.

## Contract changes (rangka-os)

- `web/app/shell.js`: `deskTop` in the side-only shell.
- `web/app/index.html`: `.ap-dtop` rules.
- `web/app/test_app.py`: `test_side_shell_top_bar`.

## Amendment (20 Nov 2026): one account menu

"The sidebar stays exactly as it is" left two menus with the same actions: the sidebar's business switcher carried
the account since contract 0019 (the side shell had no top bar then), and the new top bar brought TopNav's avatar
menu. Owners saw Perusahaan, Pakai / Ubah, the theme, Profil, Ganti akun and Keluar twice, with "Mode" as the heading
of both the theme and Pakai / Ubah.

5. **Beside the top bar the switcher is the business switcher only.** Where `.ap-dtop` shows (side-only, 768px up,
   not a drawer tablet) its menu lists the companies and nothing else. The apps are in the bar's app switcher, the
   account in its avatar menu.
6. **The avatar menu takes what only the switcher had.** "Tema dan tampilan" and "Notifikasi" (heading Pengaturan) and
   "Ulangi tur" (heading Bantuan), in both TopNavs (side-only and `top-nav`).
7. **Without the top bar nothing changes.** On a phone, a drawer tablet and the work shell's rail the switcher keeps
   the full menu (apps, companies, theme, help, account); its theme heading is now "Tema", not "Mode".
8. **The Kenalan tour's last step** ("Bantuan ada di sini") lights the avatar button where the bar shows, else the
   switcher.
