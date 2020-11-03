const { TestStep } = require("kite-common");

class StartStep extends TestStep {
  constructor(kiteBaseTest) {
    super();
    this.driver = kiteBaseTest.driver;
    this.page = kiteBaseTest.page;
  }

  stepDescription() {
    return "Start session";
  }

  async step() {
    await this.page.start();
  }
}

module.exports = StartStep;
