const {
  TestUtils,
  WebDriverFactory,
  KiteBaseTest,
  ScreenshotStep,
} = require("./node_modules/kite-common");
const { OpenPubSubUrlStep, StartStep, GetStatsStep } = require("./steps");
const { SentVideoCheck, ReceivedVideoCheck } = require("./checks");
const { PubSubPage } = require("./pages");

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

class PubSub extends KiteBaseTest {
  constructor(name, kiteConfig) {
    super(name, kiteConfig);
  }

  async testScript() {
    try {
      this.driver = await WebDriverFactory.getDriver(this.capabilities);
      this.page = new PubSubPage(this.driver);

      let openPubSubUrlStep = new OpenPubSubUrlStep(this);
      await openPubSubUrlStep.execute(this);

      await sleep(1000);

      let screenshotStep = new ScreenshotStep(this);
      await screenshotStep.execute(this);

      let startStep = new StartStep(this);
      await startStep.execute(this);

      await screenshotStep.execute(this);

      let sentVideoCheck = new SentVideoCheck(this);
      await sentVideoCheck.execute(this);

      let receivedVideoCheck = new ReceivedVideoCheck(this);
      await receivedVideoCheck.execute(this);

      if (this.getStats) {
        let getStatsStep = new GetStatsStep(this);
        await getStatsStep.execute(this);
      }

      // if (this.takeScreenshot) {
      //   let screenshotStep = new ScreenshotStep(this);
      //   await screenshotStep.execute(this);
      // }

      await this.waitAllSteps();
    } catch (e) {
      console.log(e);
    } finally {
      if (typeof this.driver !== "undefined") {
        await this.driver.quit();
      }
    }
  }
}

module.exports = PubSub;

(async () => {
  const kiteConfig = await TestUtils.getKiteConfig(__dirname);
  let test = new PubSub("PubSub test", kiteConfig);
  await test.run();
})();
