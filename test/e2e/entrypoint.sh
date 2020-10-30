#! /bin/bash

cd "${KITE_TEST_PATH}/js"
rm -rf node_modules
npm install

cd ${KITE_TEST_PATH}

/KITE/scripts/linux/path/r "configs/${KITE_CONFIG_NAME}"
