# Accessibility

Rangka is used at shop counters, in warehouses and on the owner's phone. We want every person who runs or works in a
business to be able to use it, including people who use a keyboard only, a screen reader, zoom or high contrast.

## Our commitment

We aim for [WCAG 2.2](https://www.w3.org/TR/WCAG22/) level AA in the web app (`/app/`), the work screens (such as the
POS counter) and Rangka Console (`/console/`). Rangka is in alpha and has not had a formal accessibility audit yet;
this file says what holds today and what does not.

## What holds today

- **Contrast is enforced.** The validator refuses a theme whose text drops below 4.5:1 or whose controls drop below
  3:1, in light and dark mode (`low_contrast`). Organisations cannot ship an unreadable theme by accident.
- **Phone to desktop.** Every screen is laid out for phone, tablet and desktop without sideways scrolling; touch
  screens get 44px targets ([responsive coverage](docs/responsive-coverage.md)).
- **Keyboard.** Search (`Ctrl+K` / `⌘K`), dialogs and drawers open, move and close by keyboard (`Escape` closes);
  the web app suite tests these paths.
- **Language.** Pages declare their language (`lang="id"`), so screen readers pronounce Bahasa Indonesia correctly.
- **Reduced motion.** The design system honours `prefers-reduced-motion`.

## Supported environments

The current versions of Chrome, Edge, Firefox and Safari on desktop, Android and iOS, with the screen readers built in
to those platforms (VoiceOver, TalkBack, Narrator) and NVDA on Windows.

## Known limitations

- No formal audit against WCAG 2.2 AA yet, and no automated accessibility checks (such as axe) in CI.
- Screen reader support for dense tables, kanban boards and dashboard charts is not verified.
- PDF print formats are not tagged PDFs.
- Some labels in generated screens come from `.rk` files; a module that leaves out a label gives a weaker name to
  assistive technology.

## Reporting a barrier

Open an issue with the **Accessibility barrier** template
([new issue](https://github.com/rangka-os/rangka-os/issues/new/choose)). Tell us the screen, what you tried to do, what
happened, and the browser and assistive technology you use. Barriers are labelled `accessibility` and treated as bugs.
