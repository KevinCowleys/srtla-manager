#!/usr/bin/env node
import esbuild from 'esbuild';
import { readFile, writeFile } from 'fs/promises';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const watch = process.argv.includes('--watch');

console.log('Building web assets with esbuild...');

// Bundle JavaScript modules
await esbuild.build({
    entryPoints: [join(__dirname, 'assets/js/main.js')],
    bundle: true,
    minify: true,
    sourcemap: true,
    target: 'es2020',
    outfile: join(__dirname, 'assets/app.min.js'),
    watch: watch ? {
        onRebuild(error, result) {
            if (error) console.error('JS build failed:', error);
            else console.log('JS rebuilt');
        }
    } : false
});

console.log('✓ JavaScript minified');

// Minify CSS (concatenate in correct order + minification)
const cssModules = ['base.css', 'layout.css', 'ui-elements.css', 'dependencies.css', 'network.css', 'modem-usb.css', 'wifi.css', 'camera.css', 'usbcam.css', 'modals.css', 'animations.css'];
let cssContent = '';

// Add modular CSS
for (const file of cssModules) {
    try {
        const content = await readFile(join(__dirname, 'assets/css', file), 'utf-8');
        cssContent += content + '\n';
    } catch (e) {
        console.warn(`Skipping missing CSS module: ${file}`);
    }
}

// Basic CSS minification
cssContent = cssContent
    .replace(/\/\*[\s\S]*?\*\//g, '') // Remove comments
    .replace(/\s+/g, ' ') // Collapse whitespace
    .replace(/\s*([{}:;,])\s*/g, '$1') // Remove space around syntax
    .replace(/;}/g, '}'); // Remove last semicolon in blocks

await writeFile(join(__dirname, 'assets/style.min.css'), cssContent);

console.log('✓ CSS minified');

const jsSize = (await import('fs')).statSync(join(__dirname, 'assets/app.min.js')).size;
const cssSize = (await import('fs')).statSync(join(__dirname, 'assets/style.min.css')).size;

console.log('\n✅ Build complete!');
console.log(`  - assets/app.min.js (${(jsSize / 1024).toFixed(1)} KB)`);
console.log(`  - assets/style.min.css (${(cssSize / 1024).toFixed(1)} KB)`);
console.log('\nUse app.min.js and style.min.css in production');
