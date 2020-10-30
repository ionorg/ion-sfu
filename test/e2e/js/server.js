const app = require('express')();
const http = require('http').createServer(app);
const io = require('socket.io')(http);

let stepArray = [];
let doneArray = [];

const port = process.argv[2];
const numberOfParticipant = process.argv[3];
const interval = 1000;

io.on('connection', function(socket) {
  console.log('Connected');

  socket.on('disconnect', function(){
    console.log('user disconnected');
  });

  socket.on("test finished", (id) => {
    stepArray.push(id);
  })

  socket.on("done", (id) => {
    doneArray.push(id);
  })
  
  function isFinished(){
    if(stepArray.length == numberOfParticipant) {
      socket.broadcast.emit('finished');
    }
  }
  function isDone() {
    if(doneArray.length == numberOfParticipant) {
      stepArray = [];
      doneArray = [];
    }
  }

  function ckeckStatus() {
    isFinished();
    isDone();
  }

  setInterval(ckeckStatus, interval);
});

http.listen(port, function() {
  console.log('Listening on port ' + port);
});


