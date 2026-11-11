import type { Config } from 'tailwindcss';

const config: Config = {
  content: ['./app/**/*.{ts,tsx}', './components/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#eff4ff',
          100: '#dce6ff',
          200: '#bbcefe',
          300: '#8fabfc',
          400: '#5f84f8',
          500: '#3b63f0',
          600: '#2a47d6',
          700: '#2338ac',
          800: '#1f308a',
          900: '#1d2c6e',
        },
      },
      fontFamily: {
        sans: ['var(--font-inter)', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        card: '0 1px 2px 0 rgb(15 23 42 / 0.04), 0 1px 3px 0 rgb(15 23 42 / 0.06)',
      },
    },
  },
  plugins: [],
};

export default config;
