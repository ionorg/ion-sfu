const {By, Key} = require('selenium-webdriver');
const {TestUtils, KiteTestError, Status} = require('kite-common'); 
const waitAround = TestUtils.waitAround;
const verifyVideoDisplayByIndex = TestUtils.verifyVideoDisplayByIndex;

const startButton = By.id('start');
const videoElements = By.css('video');


const getPeerConnectionScript = function() {
  return "window.pc = [];"
    + "map = APP.conference._room.rtc.peerConnections;"
    + "for(var key of map.keys()){"
    + "  window.pc.push(map.get(key).peerconnection);"
    + "}";
}

class PubSubPage {
  constructor(driver) {
    this.driver = driver;
  }

  async open(stepInfo) {
    await TestUtils.open(stepInfo);
  }

  async start() {
    let start = await this.driver.findElement(startButton);
    await start.click();
  }

  // VideoCheck with verifyVideoDisplayByIndex
  async videoCheck(stepInfo, index) {
    let checked; // Result of the verification
    let i; // iteration indicator
    let timeout = stepInfo.timeout;
    stepInfo.numberOfParticipant = parseInt(stepInfo.numberOfParticipant) + 1; // To add the first video
    
    // Waiting for all the videos
    await TestUtils.waitForVideos(stepInfo, videoElements);
    stepInfo.numberOfParticipant --; // To delete the first video

    // Check the status of the video
    // checked.result = 'blank' || 'still' || 'video'
    i = 0;
    checked = await verifyVideoDisplayByIndex(stepInfo.driver, index + 1);
    while(checked.result === 'blank' || typeof checked.result === "undefined" && i < timeout) {
      checked = await verifyVideoDisplayByIndex(stepInfo.driver, index + 1);
      i++;
      await waitAround(1000);
    }

    i = 0;
    while(i < 3 && checked.result != 'video') {
      checked = await verifyVideoDisplayByIndex(stepInfo.driver, index + 1);
      i++;
      await waitAround(3 * 1000); // waiting 3s after each iteration
    }
    return checked.result;
  }

  async getStats(stepInfo) {
    await stepInfo.driver.executeScript(getPeerConnectionScript());
    let stats = await TestUtils.getStats(stepInfo, 'kite', stepInfo.peerConnections[0]);
    return stats;
  }
}

module.exports = PubSubPage;
