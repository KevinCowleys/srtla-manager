#!/bin/bash
# Simple build script that works without Node.js
# Concatenates and does basic minification

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
ASSETS_DIR="$SCRIPT_DIR/assets"
JS_DIR="$ASSETS_DIR/js"
CSS_DIR="$ASSETS_DIR/css"

echo "Building web assets..."

# Concatenate JS modules (in order)
cat > "$ASSETS_DIR/app.bundle.js" << 'EOF'
// Auto-generated bundle - do not edit directly
(function() {
'use strict';
EOF

# Add modules in dependency order
for file in config.js api.js utils.js websocket.js chart.js modem.js usbnet.js network.js wifi.js camera.js main.js; do
    if [ -f "$JS_DIR/$file" ]; then
        echo "// === $file ===" >> "$ASSETS_DIR/app.bundle.js"
        cat "$JS_DIR/$file" | sed 's/^export //g' | sed 's/^import.*;//g' >> "$ASSETS_DIR/app.bundle.js"
    fi
done

echo "})();" >> "$ASSETS_DIR/app.bundle.js"

# Concatenate CSS files in order and do basic minification
cat "$CSS_DIR/base.css" "$CSS_DIR/layout.css" "$CSS_DIR/ui-elements.css" "$CSS_DIR/dependencies.css" "$CSS_DIR/network.css" "$CSS_DIR/modem-usb.css" "$CSS_DIR/wifi.css" "$CSS_DIR/camera.css" "$CSS_DIR/modals.css" "$CSS_DIR/animations.css" 2>/dev/null | \
    sed 's|/\*.*\*/||g' | \
    tr -d '\n' | \
    sed 's/  */ /g' | \
    sed 's/ *{ */{/g' | \
    sed 's/ *} */}/g' | \
    sed 's/ *: */:/g' | \
    sed 's/ *; */;/g' | \
    sed 's/;}/}/g' > "$ASSETS_DIR/style.bundle.css"

echo "✓ Build complete!"
echo "  - $ASSETS_DIR/app.bundle.js ($(wc -c < "$ASSETS_DIR/app.bundle.js" | numfmt --to=iec-i --suffix=B))"
echo "  - $ASSETS_DIR/style.bundle.css ($(wc -c < "$ASSETS_DIR/style.bundle.css" | numfmt --to=iec-i --suffix=B))"
