<template>
    <v-row justify="center">
        <v-col
                class="mt-4"
                cols="10"
                lg="6"
                md="10"
                sm="10"
        >
            <v-row>
                <v-col
                        class="text-center"
                        cols="12"

                >
                    <h1>Хочется дать денег на развитие проекта?</h1>
                </v-col>
            </v-row>
            <v-row>
                <v-col
                        cols="12"
                >
                    <iframe allowtransparency="true"
                            frameborder="0" height="307" scrolling="no"
                            src="https://yoomoney.ru/quickpay/shop-widget?writer=seller&targets=%D0%9C%D0%BD%D0%B5%20%D0%BE%D1%87%D0%B5%D0%BD%D1%8C%20%D0%BD%D1%80%D0%B0%D0%B2%D0%B8%D1%82%D1%81%D1%8F%20%D1%82%D0%BE%2C%20%D1%87%D1%82%D0%BE%20%D1%82%D1%8B%20%D0%B4%D0%B5%D0%BB%D0%B0%D0%B5%D1%88%D1%8C!&targets-hint=&default-sum=&button-text=13&payment-type-choice=on&comment=on&hint=&successURL=&quickpay=shop&account=41001572304361"
                            width="100%"></iframe>
                </v-col>
                <v-col
                          cols="12"
                >
                  <h3>Отправить деньги на Tinkoff</h3>
                  <p>Номер карты: 5536 9139 9418 6852</p>
                  <p>Или по <a href="https://www.tinkoff.ru/cf/634wAzuZc0Z">ссылке</a></p>
                </v-col>
                <v-col
                          cols="12"
                >
                  <h3>Отправить донат криптовалютой</h3>
                  <p>Bitcoin: bc1qrc9ltlw7mkd5reaazgtpwv3d2a70s5cj40yv97</p>
                  <p>Ethereum: 0xDF0cdEAfF312241b2f156fCc5F180F9A7ad28a20</p>
                  <p>USDT (ERC20): 0xDF0cdEAfF312241b2f156fCc5F180F9A7ad28a20</p>
                  <p>Polkadot: 13tBZbbTFGHt7b1CDa3DkowLq7cxJFvuVraNx2McZUcqQpB1</p>
                </v-col>
                <v-col
                          cols="12"
                >
                  <h3>Отправить донат через PayPal</h3>
                  <p><a href="https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62">https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62</a></p>
                </v-col>
                <v-col
                            cols="12"
                >
                  <h3>Отправить донат через BuyMeACoffee</h3>
                  <p><a href="https://www.buymeacoffee.com/aladex">https://www.buymeacoffee.com/aladex</a></p>
                </v-col>
                <v-col
                        cols="12"
                >
                    <h3>Многое поменялось со старта проекта, но он и дальше будет бесплатным. Я буду рад любым донатам</h3>
                </v-col>
                <v-col
                        class="text-center"
                        cols="12"
                >
                    <p>Есть предложения по улучшению? Напиши мне в telegram:</p>
                    <v-icon>mdi-telegram</v-icon>
                    <a href="tg://resolve?domain=aladex">@aladex</a>
                </v-col>
            </v-row>
        </v-col>
    </v-row>
</template>

<script>
    export default {
        name: "Donate",
        mounted() {
          window.scrollTo(0, 0)
        }
    }
</script>

<style scoped>

</style>