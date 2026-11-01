Release builds copy rangka-os/web and rangka-os/templates here (scripts/bundle-assets.sh) so the
binary carries the Console, design system and theme/layout templates. Development builds leave this
folder with only this file and read those directories from the workspace instead.
