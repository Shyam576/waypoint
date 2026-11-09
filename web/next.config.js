/** @type {import('next').NextConfig} */
const nextConfig = {
  // Produces a minimal, self-contained server bundle in .next/standalone
  // for Dockerfile.web, instead of requiring node_modules at runtime.
  output: 'standalone',
  // Next.js 16 writes an AGENTS.md file into web/ on every `next dev`;
  // this repo already documents AI-agent conventions at the repo root.
  agentRules: false,
  async rewrites() {
    // Proxying /api/* through the Next.js server keeps the browser's view
    // of the API same-origin, so the backend's HTTP-only session cookie
    // works without any CORS/cookie-SameSite gymnastics in local dev.
    const apiUrl = process.env.WAYPOINT_API_URL || 'http://localhost:8080';
    return [
      {
        source: '/api/:path*',
        destination: `${apiUrl}/api/:path*`,
      },
    ];
  },
};

module.exports = nextConfig;
