#!/usr/bin/env node

import { startServer } from "../src/server.js";

startServer().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
