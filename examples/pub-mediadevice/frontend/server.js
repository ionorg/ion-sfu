const express = require("express");
const app = express();

const port = 3000;

const http = require("http");
const server = http.createServer(app);

app.use(express.static(__dirname + "/public"));

server.listen(port, () => console.log(`Server is running on port ${port}`));