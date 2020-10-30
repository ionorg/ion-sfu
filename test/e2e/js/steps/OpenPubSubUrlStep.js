const { TestStep } = require("kite-common");

class OpenPubSubUrlStep extends TestStep {
  constructor(kiteBaseTest) {
    super();
    this.driver = kiteBaseTest.driver;
    this.timeout = kiteBaseTest.timeout;
    this.url = kiteBaseTest.url;
    this.page = kiteBaseTest.page;
  }

  stepDescription() {
    return "Open pub sub url";
  }

  async step() {
    await this.page.open(this);
  }
}

module.exports = OpenPubSubUrlStep;
