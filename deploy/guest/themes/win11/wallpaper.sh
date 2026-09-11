#!/bin/sh
# Apply the Win11 wallpaper to whatever screen/monitor xfdesktop reports. Runs
# from the XFCE session (via autostart) so it talks to the right xfconfd bus;
# the monitor name under Xvnc is unpredictable, so we discover it rather than
# hardcode. Falls back to screen0/monitor0 if nothing is reported yet.
WALL=/usr/share/backgrounds/warmbox-win11.png
[ -f "$WALL" ] || exit 0
sleep 2
bases=$(xfconf-query -c xfce4-desktop -l 2>/dev/null | sed -n 's#^/backdrop/\(.*\)/workspace0$#\1#p' | sort -u)
[ -n "$bases" ] || bases="screen0/monitor0"
for b in $bases; do
    xfconf-query -c xfce4-desktop -p "/backdrop/$b/workspace0/last-image" -s "$WALL" 2>/dev/null
    xfconf-query -c xfce4-desktop -p "/backdrop/$b/workspace0/image-style" -s 5 2>/dev/null
    xfconf-query -c xfce4-desktop -p "/backdrop/$b/workspace0/image-show" -s true 2>/dev/null
done
exit 0