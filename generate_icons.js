const http = require('http');
const fs = require('fs');

// We don't have canvas installed natively, but we can generate valid minimalist SVG files 
// and then inform the user that PNGs are needed if they want strict compliance, 
// OR we can change the manifest to use SVG. Actually, PWA supports SVG icons now!
