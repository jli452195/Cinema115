const fs = require('fs');
const path = 'frontend/src/components/WorkbenchHeader.vue';
let content = fs.readFileSync(path, 'utf8');

// Fix duplicated dark css rules
const styleStart = content.indexOf('<style scoped>');
if (styleStart !== -1) {
    const styleEnd = content.indexOf('</style>');
    let styles = content.substring(styleStart, styleEnd);
    
    // clean up duplicate lines
    styles = `
<style scoped>
.top-link { position: relative; border-radius: 0.6rem; padding: 0.6rem 0.65rem; font-size: 0.875rem; font-weight: 500; color: #64748b; white-space: nowrap; }
.top-link:hover { background: #f8fafc; color: #0f172a; }
.top-link.selected { background: #f1f5f9; color: #0f172a; }
.dark .top-link:hover, .dark .top-link.selected { color: #f8fafc; background: #1e293b; }
.top-link.selected::after { content: ''; position: absolute; height: 2px; bottom: -1px; left: 25%; right: 25%; background: #f59e0b; border-radius: 2px; }
.icon-button { display: flex; height: 2.25rem; width: 2.25rem; align-items: center; justify-content: center; border-radius: 0.6rem; color: #64748b; }
.icon-button:hover { background: #f1f5f9; color: #0f172a; }
.dark .icon-button:hover { background: #1e293b; color: #f8fafc; }
a:focus-visible, button:focus-visible { outline: 2px solid #f59e0b; outline-offset: 3px; }
@media (min-width: 640px) { .top-link { padding-inline: 1.25rem; } }
@media (max-width: 420px) {
  .header-inner { gap: 0.25rem; padding-inline: 0.75rem; }
  .top-link { padding-inline: 0.5rem; }
  .icon-button { width: 2rem; height: 2rem; }
}
`;
    content = content.substring(0, styleStart) + styles + '</style>\n';
    fs.writeFileSync(path, content, 'utf8');
}
